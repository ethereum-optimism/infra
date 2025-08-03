from pprint import pprint
from typing import List, Optional, TypedDict, Dict, Any, Tuple
from flask import Flask, request, jsonify, abort
import queue
import threading
import os
import time
import sys
import logging
import signal
import colorlog
from prometheus_flask_exporter import PrometheusMetrics
from werkzeug.serving import make_server
from prometheus_client import Counter, Gauge
import ipaddress
import yaml
from functools import wraps
import argparse

from lib.config_manager import ConfigManager
from lib.event_manager import EventManager, GithubEventRequest

app = Flask(__name__)

# Create a separate app for metrics
metrics_app = Flask(__name__)
metrics = PrometheusMetrics.for_app_factory()
metrics.init_app(metrics_app)

# --- Define Prometheus Metrics ---
# Existing event counters
EVENTS_RECEIVED_TOTAL = Counter(
    'webhook_events_received_total',
    'Total number of webhook events received'
)
EVENTS_VALID_TOTAL = Counter(
    'webhook_events_valid_total',
    'Total number of valid webhook events processed'
)
EVENTS_INVALID_TOTAL = Counter(
    'webhook_events_invalid_total',
    'Total number of invalid webhook events received'
)
# New Counter for tracking events by repository and event name
EVENTS_BY_REPO_AND_TYPE = Counter(
    'webhook_events_by_repo_and_type_total',
    'Total number of webhook events received by repository source and event name',
    ['repository', 'event_name']
)
# New Cache/Repo Metrics
CACHE_REFRESH_TOTAL = Counter(
    'cache_refresh_total',
    'Total number of times the configuration cache was refreshed'
)
CACHE_REPO_SEARCHES_TOTAL = Counter(
     'cache_repo_searches_total',
     'Total number of times repository list was searched/fetched during cache refresh'
)
CACHE_CONFIGURED_REPOS = Gauge(
    'cache_configured_repos',
    'Current number of repositories with configurations in the cache'
)
# New CircleCI Trigger Metrics (labeled by repository)
CIRCLECI_TRIGGERS_SENT_TOTAL = Counter(
    'circleci_triggers_sent_total',
    'Total number of CircleCI workflow triggers attempted',
    ['repository'] # Add repository label
)
CIRCLECI_TRIGGERS_SUCCEEDED_TOTAL = Counter(
    'circleci_triggers_succeeded_total',
    'Total number of successful CircleCI workflow triggers',
    ['repository'] # Add repository label
)

# --- Configure Logging using Environment Variable and colorlog ---
log_level_str = os.environ.get('LOG_LEVEL', 'INFO').upper()
log_level = getattr(logging, log_level_str, logging.INFO) # Default to INFO if invalid

# Define format strings
log_format = '%(asctime)s - %(name)s - %(levelname)s - %(message)s'
log_colors = {
    'DEBUG':    'cyan',
    'INFO':     'green',
    'WARNING':  'yellow',
    'ERROR':    'red',
    'CRITICAL': 'red,bg_white',
}
# Use colorlog for console output
colored_log_format = '%(log_color)s' + log_format

# Get the root logger
root_logger = logging.getLogger()
root_logger.setLevel(log_level) # Set the level on the root logger

# Remove existing handlers configured by basicConfig (if any) or previous runs
for handler in root_logger.handlers[:]:
    root_logger.removeHandler(handler)

# Create console handler with color
console_formatter = colorlog.ColoredFormatter(colored_log_format, log_colors=log_colors)
console_handler = colorlog.StreamHandler(sys.stdout) # Use colorlog's StreamHandler or logging.StreamHandler
console_handler.setFormatter(console_formatter)
root_logger.addHandler(console_handler)

# Create file handler without color
file_formatter = logging.Formatter(log_format)
file_handler = logging.FileHandler('webhook.log')
file_handler.setFormatter(file_formatter)
root_logger.addHandler(file_handler)

# Create logger for this specific module (optional, inherits from root)
logger = logging.getLogger(__name__)
# logger.propagate = False # Uncomment if you DON'T want __name__ logs to go to root handlers too
logger.info(f"Logging configured at level: {log_level_str}") # Log the configured level

# Queue for storing incoming webhook events
event_queue = queue.Queue()

event_manager: EventManager = None
config_manager: ConfigManager = None

# --- Process Queue Function ---
def process_queue(event_manager: EventManager, config_manager: ConfigManager):
    """Worker thread function to process events from the queue."""
    try:
        global event_queue
        logger.info("ProcessQueueThread: Starting queue processing thread")
        while True:
            try:
                event: GithubEventRequest = event_queue.get()
                logger.debug(f"ProcessQueueThread: Processing event from queue {event.event_name} {event.action_name} {event.repository_name}")
                
                # Only check for default branch and file modifications for push events
                if event.event_name == "push":
                    # Check if it's a push to the default branch
                    is_default_branch = event.json_payload["ref"] == f"refs/heads/{event.json_payload['repository']['default_branch']}"
                    # Check if the specific file was modified, removed, or deleted
                    target_file = config_manager.file_path
                    is_file_modified = any(
                        target_file in commit.get("modified", []) or
                        target_file in commit.get("removed", []) or
                        target_file in commit.get("deleted", [])
                        for commit in event.json_payload["commits"]
                    )
                    if is_file_modified:
                        refresh_cache_and_update_metrics(config_manager)

                event_manager.process_event(event)
                logger.debug("ProcessQueueThread: Event processing completed")

            except Exception as e:
                logger.error(f"ProcessQueueThread: Error in queue processing: {str(e)}", exc_info=True)
            finally:
                logger.debug("ProcessQueueThread: Event processing completed")
                event_queue.task_done()

    except Exception as e:
        logger.error(f"ProcessQueueThread: Error in queue processing: {str(e)}", exc_info=True)

# --- Background Refresh Function ---
def periodic_cache_refresh(manager: ConfigManager, interval_seconds: int):
    """Target function for the background cache refresh thread."""
    logger.info(f"Background refresh thread started. Interval: {interval_seconds} seconds.")
    while True:
        try:
            time.sleep(interval_seconds)
            refresh_cache_and_update_metrics(manager)
        except Exception as e:
            logger.error(f"Unhandled error during cache refresh loop: {e}", exc_info=True)
            time.sleep(60)  # Sleep for a minute on error before next cycle

def refresh_cache_and_update_metrics(manager: ConfigManager):
    """Common function to refresh cache and update metrics."""
    try:
        logger.info("Triggering cache refresh...")
        manager.refresh_cache()
        repo_count = manager.get_configured_repo_count()
    
        logger.info("Refresh cycle finished.")
        # Increment refresh counter
        CACHE_REFRESH_TOTAL.inc()
        # Update gauge with the current count of configured repos
        CACHE_CONFIGURED_REPOS.set(repo_count)
        logger.info(f"Cache refreshed. Configured repos gauge set to: {repo_count}")
        return True, repo_count
    except Exception as e:
        logger.error(f"Error during cache refresh: {e}", exc_info=True)
        return False, str(e)

# --- Helper Function to Get Original IP and Source Field ---
def get_original_ip(request) -> Tuple[str, str]:
    """
    Gets the original client IP address and the header field it came from.
    Checks common headers in order of preference.

    Returns:
        Tuple[str, str]: A tuple containing (ip_address, field_name).
                         field_name will be 'remote_addr' if no header is found.
    """
    # 1. Cloudflare (Most specific for your setup)
    header_name = 'CF-Connecting-IP'
    cf_ip = request.headers.get(header_name)
    if cf_ip:
        return cf_ip, header_name

    # 2. X-Forwarded-For (Common standard)
    header_name = 'X-Forwarded-For'
    x_forwarded_for = request.headers.get(header_name)
    if x_forwarded_for:
        # Take the first IP in the list
        original_ip = x_forwarded_for.split(',')[0].strip()
        return original_ip, header_name

    # 3. X-Real-IP (Alternative common header)
    header_name = 'X-Real-IP'
    x_real_ip = request.headers.get(header_name)
    if x_real_ip:
        return x_real_ip, header_name

    # 4. Fallback to the directly connected IP (Cloudflare's IP in your case)
    return request.remote_addr, 'remote_addr'

# --- Webhook and Health Endpoints ---
@app.route("/webhook", methods=["POST"])
def webhook():
    global event_manager
    # Get Cloudflare IP (directly connected IP)
    cloudflare_ip = request.remote_addr
    # Get original client IP and the field it came from
    original_ip, ip_source_field = get_original_ip(request)

    # Update initial log message
    logger.info(f"WEBHOOK POST REQUEST RECEIVED from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field})")
    try:
        EVENTS_RECEIVED_TOTAL.inc()
        # Update subsequent logs
        logger.info(f"Processing webhook request from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field})")
        github_event_request = event_manager.validate_request(request)
        if not github_event_request:
            EVENTS_INVALID_TOTAL.inc()
            # Update error log
            logger.error(f"Invalid request from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field})")
            return jsonify({"error": "Invalid request"}), 400
        else:
            
            EVENTS_VALID_TOTAL.inc()
            event_queue.put(github_event_request)
            # Update success log
            logger.info(f"Request queued successfully from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field})")
            return jsonify({"status": "queued"}), 200

    except ValueError as e:
        EVENTS_INVALID_TOTAL.inc()
        # Update warning log
        logger.warning(f"Validation error from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field}): {str(e)}")
        return jsonify({"error": str(e)}), 400
    except Exception as e:
        EVENTS_INVALID_TOTAL.inc()
        # Update error log
        logger.error(f"Unexpected error from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field}): {str(e)}", exc_info=True)
        return jsonify({"error": f"Unexpected error: {str(e)}"}), 500

@metrics_app.route('/healthz')
def health_check():
    cloudflare_ip = request.remote_addr
    original_ip, ip_source_field = get_original_ip(request)
    logger.info(f"Health check request from Source IP: {cloudflare_ip}, Original IP: {original_ip} (Source: {ip_source_field})")
    return {'status': 'healthy'}, 200

# --- Graceful Shutdown ---
def graceful_shutdown(signum, frame):
    # Perform cleanup
    logger.info("Received SIGTERM, shutting down gracefully.")
    # Add any specific cleanup needed for threads or resources here if necessary
    sys.exit(0)

signal.signal(signal.SIGTERM, graceful_shutdown)

# --- Function to run the metrics server ---
def run_metrics_server(port: int):
    """Runs the metrics server on the configured port."""
    logger.info(f"Starting metrics server on port {port}")

    try:
        # Serve the original metrics_app (which has /metrics via PrometheusMetrics)
        metrics_server = make_server('0.0.0.0', port, metrics_app)  # nosec B104
        metrics_server.serve_forever()
    except Exception as e:
        logger.error(f"Metrics server failed: {e}", exc_info=True)

# --- Helper Function to print usage/help ---
def print_usage():
    """Prints usage and help information."""
    help_text = """
GitHub Event Manager

This application listens for GitHub webhook events, validates them, and triggers
CircleCI workflows based on configurations found in the repositories.

Command-line Arguments:
  -h, --help            Show this help message and exit.
  --config-file-path CONFIG_FILE_PATH
                        Path to the configuration file within repositories.
                        Overrides env: CONFIG_FILE_PATH
                        (Default: '.circleci/github-event-handler.yml')
  --circleci-token CIRCLECI_TOKEN
                        (Required) Your CircleCI API token.
                        Overrides env: CIRCLECI_TOKEN
  --github-token GITHUB_TOKEN
                        (Required) Your GitHub API token.
                        Overrides env: GITHUB_TOKEN
  --valid-organization VALID_ORGANIZATION
                        (Required) The GitHub organization this app will operate on.
                        Overrides env: VALID_ORGANIZATION
  --webhook-secret WEBHOOK_SECRET
                        (Required) The secret used to validate GitHub webhooks.
                        Overrides env: WEBHOOK_SECRET
  --cache-refresh-interval CACHE_REFRESH_INTERVAL
                        Interval in seconds for refreshing the repository config cache.
                        Overrides env: CACHE_REFRESH_INTERVAL (Default: 600)
  --app-log-level APP_LOG_LEVEL   Logging level for the application (DEBUG, INFO, WARNING, ERROR, CRITICAL).
                        Overrides env: APP_LOG_LEVEL (Default: INFO)
  --metrics-port METRICS_PORT
                        Port for the Prometheus metrics server.
                        Overrides env: METRICS_PORT (Default: 9090)

Environment Variables (used if corresponding command-line argument is not set):
  CONFIG_FILE_PATH      (Default: '.circleci/github-event-handler.yml')
  CIRCLECI_TOKEN        (Required)
  GITHUB_TOKEN          (Required)
  VALID_ORGANIZATION    (Required)
  WEBHOOK_SECRET        (Required)
  CACHE_REFRESH_INTERVAL (Default: 600)
  APP_LOG_LEVEL         Logging level for the application (Default: INFO)
  METRICS_PORT          (Default: 9090)

Usage example:
  python src/main.py --valid-organization my-org --circleci-token tok1 --github-token tok2 --webhook-secret secret123

It's recommended to set secrets (tokens, webhook_secret) via environment variables
rather than command-line arguments for better security.
    """
    print(help_text)

def main():
    global event_manager, config_manager, root_logger, logger, log_level_str, log_level

    # --- Argument Parsing ---
    parser = argparse.ArgumentParser(description="GitHub Event Manager", add_help=False)
    parser.add_argument(
        '-h', '--help',
        action='store_true',
        help='Show this help message and exit.'
    )
    parser.add_argument(
        '--config-file-path',
        type=str,
        metavar='CONFIG_FILE_PATH',
        help='Path to the configuration file in repositories. Overrides env: CONFIG_FILE_PATH'
    )
    parser.add_argument(
        '--circleci-token',
        type=str,
        metavar='CIRCLECI_TOKEN',
        help='Your CircleCI API token. Overrides env: CIRCLECI_TOKEN'
    )
    parser.add_argument(
        '--github-token',
        type=str,
        metavar='GITHUB_TOKEN',
        help='Your GitHub API token. Overrides env: GITHUB_TOKEN'
    )
    parser.add_argument(
        '--valid-organization',
        type=str,
        metavar='VALID_ORGANIZATION',
        help='The GitHub organization this app will operate on. Overrides env: VALID_ORGANIZATION'
    )
    parser.add_argument(
        '--webhook-secret',
        type=str,
        metavar='WEBHOOK_SECRET',
        help='The secret used to validate GitHub webhooks. Overrides env: WEBHOOK_SECRET'
    )
    parser.add_argument(
        '--cache-refresh-interval',
        type=int,
        metavar='CACHE_REFRESH_INTERVAL',
        help='Interval in seconds for refreshing the config cache. Overrides env: CACHE_REFRESH_INTERVAL'
    )
    parser.add_argument(
        '--app-log-level',
        type=str,
        metavar='APP_LOG_LEVEL',
        choices=['DEBUG', 'INFO', 'WARNING', 'ERROR', 'CRITICAL'],
        help='Logging level for the application. Overrides env: APP_LOG_LEVEL'
    )
    parser.add_argument(
        '--metrics-port',
        type=int,
        metavar='METRICS_PORT',
        help='Port for the Prometheus metrics server. Overrides env: METRICS_PORT'
    )
    
    args, unknown_args = parser.parse_known_args()

    if args.help:
        print_usage()
        sys.exit(0)

    # --- Determine Configuration Values (Arg > Env > Default) ---

    def get_config_value(arg_value, env_name, default_value=None, is_int=False, is_required=False, var_name_for_log=""):
        val = arg_value
        source = "argument"
        if val is None:
            val = os.environ.get(env_name)
            source = f"env:{env_name}"
            if val is None:
                val = default_value
                source = "default"
        
        if is_required and val is None:
            logger.critical(f"Configuration Error: {var_name_for_log or env_name} is required but not set via argument or environment variable.")
            print_usage()
            sys.exit(1)

        if val is not None and is_int:
            try:
                return int(val), source
            except ValueError:
                logger.critical(f"Configuration Error: {var_name_for_log or env_name} must be an integer. Got '{val}' from {source}.")
                sys.exit(1)
        return val, source

    circleci_token, circleci_token_source = get_config_value(args.circleci_token, 'CIRCLECI_TOKEN', is_required=True, var_name_for_log="CircleCI Token")
    github_token, github_token_source = get_config_value(args.github_token, 'GITHUB_TOKEN', is_required=True, var_name_for_log="GitHub Token")
    valid_organization, valid_org_source = get_config_value(args.valid_organization, 'VALID_ORGANIZATION', is_required=True, var_name_for_log="Valid Organization")
    webhook_secret, webhook_secret_source = get_config_value(args.webhook_secret, 'WEBHOOK_SECRET', is_required=True, var_name_for_log="Webhook Secret")
    
    cache_refresh_interval, cache_interval_source = get_config_value(args.cache_refresh_interval, 'CACHE_REFRESH_INTERVAL', 60*10, is_int=True)
    config_file_path, config_file_source = get_config_value(args.config_file_path, 'CONFIG_FILE_PATH', ".circleci/github-event-handler.yml")
    log_level_arg, log_level_source = get_config_value(args.app_log_level, 'APP_LOG_LEVEL', 'INFO')
    metrics_port, metrics_port_source = get_config_value(args.metrics_port, 'METRICS_PORT', 9090, is_int=True)

    # --- Reconfigure Logging if log_level was changed by arg or env ---
    # The initial logging setup uses LOG_LEVEL env var or 'INFO'.
    # We need to adjust this to check APP_LOG_LEVEL first for the application's own logging.
    # Gunicorn will still control its own logging separately.

    # Initial log_level_str is set near the top of the file from LOG_LEVEL (for Gunicorn) or 'INFO'
    # We update it here if APP_LOG_LEVEL is specified for the application's internal logging.
    
    # Get the application-specific log level
    # log_level_str was already set up based on 'LOG_LEVEL' env var (potentially for Gunicorn)
    # Now, let's determine the application's specific log level using the new variable
    app_log_level_str_from_config, app_log_level_source = get_config_value(args.app_log_level, 'APP_LOG_LEVEL', 'INFO')
    app_log_level_val = getattr(logging, app_log_level_str_from_config.upper(), logging.INFO)

    # Set the application's root logger level
    # The global `log_level_str` and `log_level` should reflect the app's level
    log_level_str = app_log_level_str_from_config.upper()
    log_level = app_log_level_val
    
    root_logger.setLevel(log_level) # Set the level on the root logger
    # The individual logger `logger = logging.getLogger(__name__)` will inherit this.
    # Also, ensure the initial log message reflects the correct setting.
    # The `logger.info(f"Logging configured at level: {log_level_str}")`
    # that runs early in the script might print the Gunicorn-influenced level.
    # We'll log the app-specific level after this setup.

    # --- Log final configuration sources ---
    logger.info(f"Using CircleCI Token (Source: {circleci_token_source})")
    logger.info(f"Using GitHub Token (Source: {github_token_source})")
    logger.info(f"Using Valid Organization: {valid_organization} (Source: {valid_org_source})")
    logger.info(f"Using Webhook Secret (Source: {webhook_secret_source})")
    logger.info(f"Using Config File Path: {config_file_path} (Source: {config_file_source})")
    logger.info(f"Using Cache Refresh Interval: {cache_refresh_interval}s (Source: {cache_interval_source})")
    # Log the application's log level
    logger.info(f"Application Log Level set to: {log_level_str} (Source: {app_log_level_source})")
    logger.info(f"Using Metrics Port: {metrics_port} (Source: {metrics_port_source})")


    # current_log_level = logging.getLogger().getEffectiveLevel() # This will now be the app's log level
    log_level_name = logging.getLevelName(log_level) # Use the app's log_level


    if not all([circleci_token, github_token, valid_organization, webhook_secret]):
        # This check is somewhat redundant due to get_config_value's is_required,
        # but kept for safety. It will be caught by get_config_value earlier.
        logger.critical("Error: Critical environment variables/arguments (tokens, org, secret) must be set.")
        sys.exit(1)

    try:
        logger.info(f"MAIN INIT: Starting application with log level: {log_level_name}")
        logger.info(f"MAIN INIT: Valid organization: {valid_organization}")

        # Instantiate ConfigManager, passing the repo search counter
        config_manager = ConfigManager(
            valid_organization,
            github_token,
            CACHE_REPO_SEARCHES_TOTAL,
            file_path=config_file_path
        )
        logger.info(f"MAIN INIT: Initializing Config Manager Cache")
        config_manager.refresh_cache()
        initial_repo_count = config_manager.get_configured_repo_count()
        CACHE_CONFIGURED_REPOS.set(initial_repo_count)
        logger.info(f"MAIN INIT: Initial cache loaded. Configured repos: {initial_repo_count}")

        # Instantiate EventManager, passing the CircleCI counters
        event_manager = EventManager(
            valid_organization,
            webhook_secret,
            circleci_token,
            config_manager,
            CIRCLECI_TRIGGERS_SENT_TOTAL,      # Pass sent counter
            CIRCLECI_TRIGGERS_SUCCEEDED_TOTAL, # Pass succeeded counter
            EVENTS_BY_REPO_AND_TYPE           # Pass events by repo and type counter
        )

        # Start the background cache refresh thread
        cache_refresh_thread = threading.Thread(target=periodic_cache_refresh,  args=(config_manager, cache_refresh_interval), name="CacheRefreshThread", daemon=True)
        cache_refresh_thread.start()
        logger.info(f"MAIN INIT: Cache Refresh thread started (Interval: {cache_refresh_interval}s)")

        # Start the worker thread
        process_queue_thread = threading.Thread(target=process_queue,  args=(event_manager,config_manager), name="ProcessQueueThread", daemon=True)
        process_queue_thread.start()
        logger.info("MAIN INIT: Process Queue thread started")

        # Start metrics server in a separate thread, passing the determined metrics_port
        metrics_thread = threading.Thread(target=run_metrics_server, args=(metrics_port,), name="MetricsServerThread", daemon=True)
        metrics_thread.start()
        logger.info("MAIN INIT: Metrics Server thread started")

        logger.info("MAIN INIT: Application setup complete. Ready for Gunicorn.")

    except Exception as e:
        logger.error(f"MAIN INIT: Error in main setup: {str(e)}", exc_info=True)
        sys.exit(1) # Exit if setup fails

# Run main initialization logic when the module is imported by Gunicorn
main()

# The 'app' variable is now configured and ready for Gunicorn to serve.
# The metrics server runs in a separate thread started by main().
  
    