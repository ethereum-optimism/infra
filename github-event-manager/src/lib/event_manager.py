import hashlib
import hmac
import json
import logging
from pprint import pprint
from typing import Dict, Optional, Union, Any
from venv import logger

from .circleci_proxy import CircleCIProxy
from .config_manager import ConfigManager
from .repo_config import BaseRepoConfig
from .common import GithubEventRequest, RepoMetadata, generate_test_data_bytes

# Import Counter for type hinting
from prometheus_client import Counter

# Get logger for this module
logger = logging.getLogger(__name__)
    
class EventManager:

    def __init__(
        self,
        valid_organization: str,
        github_webhook_secret: str,
        circleci_token: str,
        config_manager: ConfigManager,
        triggers_sent_counter: Counter,      # Add counter param
        triggers_succeeded_counter: Counter, # Add counter param
        events_received_counter: Counter    # New counter for tracking received events
    ) -> None:
        """Initialize GitHubEventManager.
        
        Args:
            github_webhook_secret: GitHub webhook secret for verification
        """
        self.valid_organization: str = valid_organization
        self.github_webhook_secret: str = github_webhook_secret
        self.circleci_token: str = circleci_token
        self.config_manager: ConfigManager = config_manager

        self.circleci_proxy = CircleCIProxy(self.valid_organization, self.circleci_token)

        # Store counter instances
        self.triggers_sent_counter = triggers_sent_counter
        self.triggers_succeeded_counter = triggers_succeeded_counter
        self.events_received_counter = events_received_counter


    def process_event(self, event: GithubEventRequest) -> None:
        """
        Process a GitHub webhook event by triggering appropriate CircleCI workflows.
        
        This function handles the core business logic of the application:
        1. Logs the incoming event information
        2. Retrieves all repository configurations from the config manager
        3. For each repository configuration:
           - Checks if the event is allowed based on config rules
           - Extracts parameters from the event payload using configured mappings
           - Triggers the appropriate CircleCI workflow with extracted parameters
           - Records metrics for sent and successful triggers
        
        The function processes all matching repository configurations, allowing
        a single event to trigger workflows in multiple repositories if their
        configurations match the event criteria.
        
        Args:
            event: A validated GithubEventRequest object containing event details
                  including type, action, payload, and repository information
        
        Returns:
            None
        
        Raises:
            No exceptions are raised as errors are caught and logged internally
        """

        logger.info(f"EVENT_MANAGER: processing event: {event.event_name} {event.action_name} from repository: {event.repository_name} ")
        
        # Increment the events received counter with repository source and event name labels
        self.events_received_counter.labels(repository=event.repository_name, event_name=event.event_name).inc()
        logger.debug(f"EVENT_MANAGER: Incremented events_received counter for repo {event.repository_name}, event {event.event_name}.")
                
        repo_configs = self.config_manager.get_all_repo_configs()
        try:

            for repo_config in repo_configs:
                config:BaseRepoConfig=repo_config["configuration"]
                config_repo_metadata:RepoMetadata=repo_config["repo_metadata"]
                logger.info(f"EVENT_MANAGER: checking  {event.event_name} {event.action_name} for repository config {config_repo_metadata['repo_name']}")

                if config.is_event_allowed(event):
                    
                    default_branch = config_repo_metadata["repo_default_branch"]
                    logger.info(f"EVENT_MANAGER: Repository {config_repo_metadata['repo_name']} found in config")
                    
                    (paylod_to_send, circleci_parameters) = config.get_parameter_mappings(event)
                    logger.info(f"EVENT_MANAGER: CIRCLECI_PROXY SENDING PAYLOAD: {paylod_to_send} to {default_branch} for {event.event_name} {event.action_name}")
                    logger.info(f"EVENT_MANAGER: CIRCLECI_PROXY SENDING PARAMETERS: {circleci_parameters} to {default_branch} for {event.event_name} {event.action_name}")
                   
                    # Increment sent counter before attempting the trigger
                    self.triggers_sent_counter.labels(repository=config_repo_metadata["repo_name"]).inc()
                    logger.debug(f"EVENT_MANAGER: Incremented triggers_sent counter for repo {config_repo_metadata['repo_name']}.")

                    # Trigger the CircleCI workflow
                    response_status, response_reason = self.circleci_proxy.trigger_circleci_workflow(config_repo_metadata["repo_name"], default_branch, event.event_name, event.action_name, paylod_to_send, circleci_parameters, event.repository_name)
                    logger.info(f"EVENT_MANAGER: CIRCLECI_PROXY: Trigger result for {config_repo_metadata['repo_name']} ... Status: {response_status}, Reason: {response_reason}")
                    
                    # Increment succeeded counter if status indicates success (e.g., 2xx)
                    if 200 <= response_status < 300:
                        self.triggers_succeeded_counter.labels(repository=config_repo_metadata["repo_name"]).inc()
                        logger.debug(f"EVENT_MANAGER: Incremented triggers_succeeded counter for repo {config_repo_metadata['repo_name']}.")
                    else:
                        logger.warning(f"EVENT_MANAGER: CircleCI trigger failed or did not return success status for repo {config_repo_metadata['repo_name']}. Status: {response_status}")

        except Exception as e:
            logger.error(f"EVENT_MANAGER: Error processing event: {str(e)}", exc_info=True)
        


    def validate_request(self, request) -> Optional[GithubEventRequest]:
        """
        Validate and process the incoming GitHub webhook request.
        
        This function performs several validation steps on an incoming webhook request:
        1. Verifies that required headers are present (X-GitHub-Event, X-Hub-Signature-256, Content-Type)
        2. Extracts and validates the payload based on the Content-Type
        3. Verifies the signature using the webhook secret
        4. Validates that the request comes from the configured organization
        
        Args:
            request: The HTTP request object containing headers and payload
            
        Returns:
            GithubEventRequest: A structured object containing the validated event data
            None: If validation fails at any step
            
        Raises:
            ValueError: If required headers are missing or Content-Type is invalid
        """
        logger.debug("EVENT_MANAGER: Processing incoming request")
        
        signature_header = request.headers.get("X-Hub-Signature-256", "")
        content_type = request.headers.get("Content-Type", "")
        event_name = request.headers.get("X-GitHub-Event", "")
        
        if not event_name:
            logger.error("EVENT_MANAGER: Missing X-GitHub-Event header")
            raise ValueError("Missing X-GitHub-Event header")
        
        if not signature_header:
            logger.error("EVENT_MANAGER: Missing X-Hub-Signature-256 header")
            raise ValueError("EVENT_MANAGER: Missing X-Hub-Signature-256 header")

        if not content_type:
            logger.error("EVENT_MANAGER: Missing content type header")
            raise ValueError("EVENT_MANAGER: Missing Content-Type header")

        # Get payload based on content type
        if content_type == "application/json":
            payload_body = request.get_data()
            logger.debug("EVENT_MANAGER: Received JSON payload")
        elif content_type == "application/x-www-form-urlencoded":
            form_payload = request.form.get("payload", "")
            if not form_payload:
                logger.error("EVENT_MANAGER: Missing payload in form data")
                raise ValueError("EVENT_MANAGER: Missing payload in form data")
            payload_body = form_payload.encode('utf-8')
            logger.debug("EVENT_MANAGER: Received form payload")
        else:
            logger.error(f"EVENT_MANAGER: Invalid content type: {content_type}")
            raise ValueError("EVENT_MANAGER: Content-Type must be application/json or application/x-www-form-urlencoded")

        logger.info(f"EVENT_MANAGER: event_name: {event_name} signature_header: {signature_header} content_type: {content_type}")

        if not self.is_signature_valid(payload_body, signature_header, event_name):
            logger.error(f"EVENT_MANAGER: signature_invalid: event_name: {event_name}")
            return None
        else:
            logger.info(f"EVENT_MANAGER: signature_valid for event_name: {event_name}")
            if isinstance(payload_body, bytes):
                try:
                    payload_body = payload_body.decode('utf-8')
                    # Parse JSON
                    json_payload = json.loads(payload_body)

                    enterprise = json_payload.get("enterprise", {}).get("name", "")  # Assuming the enterprise name is in the event data
                    organization = json_payload.get("organization", {}).get("login", "")  # Assuming the organization name is in the event data
                    
                    #these fields must all be present in the request and must all be equal to the valid_organization
                    user = json_payload.get("sender", {}).get("login", "")
                    action = json_payload.get("action", "")
                    
                    if enterprise != self.valid_organization or organization != self.valid_organization:
                        
                        logger.warn(f"Only accepting events from: {self.valid_organization} while got enterprise: {enterprise}, organization: {organization}, user: {user}, event_name: {event_name}, action: {action} ")
                        return None

                    return GithubEventRequest(event_name, action, payload_body, json_payload)
        
                except Exception as e:
                    logger.error(f"EVENT_MANAGER: load_json_payload: event_name: {event_name} error: {e}")
                    return None
            else:
                return None 


    def is_signature_valid(self, payload_body: bytes, signature_header: str, event_name: str = None) -> bool:
        """
        Verify that a webhook payload was sent from GitHub by validating the SHA256 signature.
        
        GitHub includes a signature header (X-Hub-Signature-256) with each webhook delivery.
        This function validates that signature by:
        1. Computing an HMAC SHA256 of the payload using the configured webhook secret
        2. Comparing this computed signature with the one provided in the request header
        
        This validation ensures that:
        - The payload was actually sent by GitHub (or someone with knowledge of the secret)
        - The payload has not been tampered with during transit
        
        Args:
            payload_body: The raw bytes content of the webhook payload
            signature_header: The GitHub signature from X-Hub-Signature-256 header
            event_name: Optional event name for logging purposes
        
        Returns:
            bool: True if the signature is valid, False otherwise
        
        Security note:
            This function uses hmac.compare_digest() for constant-time comparison
            to prevent timing attacks when validating signatures.
        """

        #save the payload to a file and signature to a file with the name of the repository followed by the event name and action name
        payload_body_json = json.loads(payload_body.decode('utf-8'))
        action_name = payload_body_json.get("action", "")
        repository_name = payload_body_json.get("repository", {}).get("name", "")
        signature_header_object = {"data": signature_header}
        payload_body_object = {"data": payload_body.decode('utf-8')}
        with open(f"{repository_name}_{event_name}_{action_name}_body.json", "w") as f:
            f.write(json.dumps(payload_body_object))
        with open(f"{repository_name}_{event_name}_{action_name}_header.json", "w") as f:
            f.write(json.dumps(signature_header_object))

        if not signature_header:
            return False        
        
        hash_object = hmac.new(
            self.github_webhook_secret.encode('utf-8'),
            msg=payload_body,
            digestmod=hashlib.sha256
        )
        expected_signature = "sha256=" + hash_object.hexdigest()
        is_valid = hmac.compare_digest(expected_signature, signature_header)
        return is_valid

