import base64
import threading
import time
import traceback
from typing import Any, Dict, Optional, Tuple, TypedDict, List
import yaml
import logging
from pprint import pprint
import requests # Import requests
from .common import RepoConfig, RepoMetadata, generate_test_data
from .repo_config import BaseRepoConfig

logger = logging.getLogger(__name__) # Use module-level logger


class ConfigManager:
    CACHE_DURATION = 60 * 10 # 10 minutes
    FILE_SIZE_LIMIT = 1024 * 512 # 512KB
    MAX_WORKERS = 10
    CALLS = 30
    RATE_LIMIT = 60
    GITHUB_GQL_ENDPOINT = "https://api.github.com/graphql"
    BATCH_SIZE = 50 # Number of repos to check per GraphQL file query
    QUERY_REPOSITORIES= """
        query($org: String!, $cursor: String, $filePath: String!) {
          organization(login: $org) {
            repositories(first: 100, after: $cursor, ownerAffiliations: [OWNER], isFork: false, isLocked: false) {
              nodes {
                name
                defaultBranchRef {
                  name
                }
                configFile: object(expression: $filePath) {
                  ... on Blob {
                    oid
                    byteSize
                  }
                }
              }
              pageInfo {
                endCursor
                hasNextPage
              }
            }
          }
        }
        """

    def __init__(self, github_organization: str, github_token: str, repo_search_counter, file_path: str=".circleci/github-event-handler.yml"):
        self.github_organization: str = github_organization
        self.github_token: str = github_token
        self.gql_headers = {
            "Authorization": f"bearer {self.github_token}",
            "Content-Type": "application/json",
        }
        self.cached_config: Dict[str, RepoConfig] = {}
        self.cache_locks: Dict[str, threading.Lock] = {}
        self.cache_lock = threading.Lock()  # Add the main cache lock
        self.repo_search_counter = repo_search_counter
        self.file_path = file_path

    def _execute_graphql_query(self, query: str, variables: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        """Helper function to execute a GraphQL query."""
        payload = {"query": query, "variables": variables}
        try:
            response = requests.post(self.GITHUB_GQL_ENDPOINT, headers=self.gql_headers, json=payload, timeout=30)
            response.raise_for_status()  # Raise HTTPError for bad responses (4xx or 5xx)
            result = response.json()
            if "errors" in result:
                logger.error(f"CONFIG_MANAGER: GraphQL query returned errors: {result['errors']}")
                return None
            return result.get("data")
        except requests.exceptions.RequestException as e:
            logger.error(f"CONFIG_MANAGER: Error executing GraphQL query: {e}")
            return None
        except Exception as e:
            logger.error(f"CONFIG_MANAGER: Unexpected error during GraphQL execution: {e}", exc_info=True)
            return None

    def _get_all_organization_repos(self) -> List[Dict[str, str]]:
        """Fetches all repository names and default branches for the organization using GraphQL pagination."""
        all_repos = []
        has_next_page = True
        cursor = None
        query = ConfigManager.QUERY_REPOSITORIES
        logger.info(f"CONFIG_MANAGER: Fetching all repository names for org '{self.github_organization}'...")
        page_count = 0
        while has_next_page:
            page_count += 1
            logger.debug(f"CONFIG_MANAGER: Fetching repository page {page_count}...")
            # Create the filepath expression for GraphQL
            file_path_expr = f"HEAD:{self.file_path}"
            variables = {"org": self.github_organization, "cursor": cursor, "filePath": file_path_expr}
            data = self._execute_graphql_query(query, variables)

            if not data or "organization" not in data or not data["organization"] or "repositories" not in data["organization"]:
                logger.error("CONFIG_MANAGER: Failed to fetch repositories or received unexpected GraphQL structure.")
                has_next_page = False # Stop pagination on error
                break # Exit loop

            repos_data = data["organization"]["repositories"]
            page_info = repos_data["pageInfo"]
            has_next_page = page_info["hasNextPage"]
            cursor = page_info["endCursor"]

            # Process nodes from the current page
            for repo_node in repos_data.get("nodes", []):
                if repo_node.get("configFile"):
                    # Ensure repo name and default branch exist before adding
                    if repo_node and repo_node.get("name") and repo_node.get("defaultBranchRef") and repo_node["defaultBranchRef"].get("name"):
                        all_repos.append({
                            "name": repo_node["name"],
                            "default_branch": repo_node["defaultBranchRef"]["name"],
                            "configFile": repo_node.get("configFile")  # Include the configFile data
                        })
                    else:
                        logger.warning(f"CONFIG_MANAGER: Skipping repo node due to missing data: {repo_node}")
                else:
                    logger.debug(f"CONFIG_MANAGER: Skipping repo node due to missing configFile: {repo_node}")

            logger.debug(f"CONFIG_MANAGER: Fetched page {page_count}. Has next: {has_next_page}. Total repos so far: {len(all_repos)}")
            time.sleep(0.1)  # small sleep to avoid hitting secondary rate limits


        logger.info(f"CONFIG_MANAGER: Finished fetching repository names. Total found: {len(all_repos)}")
        return all_repos

    def _check_files_in_batch(self, repos_batch: List[Dict[str, str]]) -> List[RepoMetadata]:
        """Checks for the config file in a batch of repositories using GraphQL."""
        found_configs = []
        if not repos_batch:
            return found_configs

        # Build the GraphQL query dynamically based on the batch size
        query_parts = []
        variables = {"org": self.github_organization}
        repo_aliases = {}

        for i, repo_info in enumerate(repos_batch):
            repo_name = repo_info["name"]
            alias = f"repo{i}"
            var_name = f"repoName{i}"
            repo_aliases[alias] = repo_name # Store alias -> actual name mapping
            variables[var_name] = repo_name
            query_parts.append(f"""
            {alias}: repository(name: ${var_name}) {{
              name
              defaultBranchRef {{
                name
              }}
              configFile: object(expression: "HEAD:{self.file_path}") {{
                 ... on Blob {{
                   oid
                   byteSize
                 }}
              }}
            }}
            """)

        query = f"""
        query($org: String!, {', '.join(f'${var_name}: String!' for var_name in variables if var_name != "org")}) {{
          organization(login: $org) {{
            {' '.join(query_parts)}
          }}
        }}
        """

        logger.debug(f"CONFIG_MANAGER: Executing batched file check for {len(repos_batch)} repos.")
        data = self._execute_graphql_query(query, variables)

        if not data or "organization" not in data or not data["organization"]:
            logger.error("CONFIG_MANAGER: Failed to check files in batch or received unexpected GraphQL structure.")
            return found_configs

        org_data = data["organization"]
        for alias, repo_name in repo_aliases.items():
             repo_result = org_data.get(alias)
             if not repo_result:
                 logger.warning(f"CONFIG_MANAGER: No result found for alias {alias} (repo: {repo_name}) in batched response.")
                 continue

             config_file_data = repo_result.get("configFile")
             default_branch_ref = repo_result.get("defaultBranchRef")
             default_branch_name = default_branch_ref.get("name") if default_branch_ref else None

             # Check if file exists, has size within limit, and default branch is known
             if (config_file_data and
                 config_file_data.get("oid") and
                 config_file_data.get("byteSize") is not None and
                 config_file_data["byteSize"] <= ConfigManager.FILE_SIZE_LIMIT and
                 default_branch_name):

                 found_configs.append(RepoMetadata(
                     repo_name=repo_name,
                     repo_default_branch=default_branch_name,
                     repo_owner=self.github_organization,
                     repo_owner_type="Organization",
                     file_path=self.file_path,
                     file_sha=config_file_data["oid"],
                     file_size=config_file_data["byteSize"],
                 ))
                 logger.debug(f"CONFIG_MANAGER: Found valid config file in repo: {repo_name}")
             else:
                 if default_branch_name and config_file_data and config_file_data.get("byteSize", 0) > ConfigManager.FILE_SIZE_LIMIT:
                      logger.info(f"CONFIG_MANAGER: Config file found but too large in {repo_name} ({config_file_data.get('byteSize')} > {ConfigManager.FILE_SIZE_LIMIT})")
                 elif not default_branch_name:
                     logger.warning(f"CONFIG_MANAGER: Could not determine default branch for repo {repo_name} during file check.")
                 # No need to log if file simply doesn't exist, that's the common case.

        return found_configs

    # --- Rewritten search_all_repos using GraphQL ---
    def search_all_repos(self) -> List[RepoMetadata]:
        """
        Searches for all repositories in the org that have the config file using GraphQL.
        1. Gets all repo names.
        2. Batches repo names to check for file existence.
        """
        # 1. Get all repository names and default branches
        all_org_repos = self._get_all_organization_repos()
        if not all_org_repos:
            return []
        
        logger.info(f"CONFIG_MANAGER: Found {len(all_org_repos)} repositories with the config file.")
        logger.info(f"CONFIG_MANAGER: Repositories: {all_org_repos}")

        # 2. Check for config file in batches
        repos_with_config = []
        num_repos = len(all_org_repos)
        logger.info(f"CONFIG_MANAGER: Checking for config file '{self.file_path}' in {num_repos} repositories (batch size: {self.BATCH_SIZE})...")

        for i in range(0, num_repos, self.BATCH_SIZE):
            batch = all_org_repos[i:i + self.BATCH_SIZE]
            logger.info(f"CONFIG_MANAGER: Processing batch {i // self.BATCH_SIZE + 1}/{(num_repos + self.BATCH_SIZE - 1) // self.BATCH_SIZE}...")
            results = self._check_files_in_batch(batch)
            repos_with_config.extend(results)
            time.sleep(0.1) # sleep between batches to avoid rate limits

        logger.info(f"CONFIG_MANAGER: GraphQL search complete. Found {len(repos_with_config)} repositories with the config file.")
        logger.info(f"CONFIG_MANAGER: Repositories with config file: {repos_with_config}")
        return repos_with_config

    def refresh_cache(self):
        """Refresh the cache for all repositories found via GraphQL search."""
        with self.cache_lock:
            try:
                # Increment search counter before performing the search
                self.repo_search_counter.inc()
                logging.info("CONFIG_MANAGER: Incremented repo search counter.")

                # Use the NEW GraphQL-based search
                repos = self.search_all_repos()
                if not repos:
                    logging.warning(f"CONFIG_MANAGER: No repositories found with config file during cache refresh search.")
                    return

                logging.info(f"CONFIG_MANAGER: Refreshing cache based on {len(repos)} found repositories with config files.")
                new_cache: Dict[str, RepoConfig] = {} # Build a new cache dictionary

                # Process found repositories
                for repo in repos:
                    logging.info(f"CONFIG_MANAGER: Processing repo: {repo}")
                    repo_name = repo["repo_name"]
                    # Check if config needs fetching (new repo or updated SHA)
                    should_fetch = (
                        repo_name not in self.cached_config or
                        self.cached_config[repo_name]["repo_metadata"]["file_sha"] != repo["file_sha"]
                    )

                    if should_fetch:
                        logging.info(f"CONFIG_MANAGER: Fetching configuration content for {repo_name}...")
                        config_data = self.fetch_configuration_from_repository(
                            repo["repo_name"], repo["repo_default_branch"]
                        )
                        if config_data is not None:
                            new_cache[repo_name] = {
                                "repo_metadata": repo,
                                "last_fetched": time.time(),
                                "configuration": BaseRepoConfig.create_from_dict(repo_name, config_data)
                            }
                            logging.info(f"CONFIG_MANAGER: Successfully cached configuration for {repo_name}.")
                        else:
                            logging.warning(f"CONFIG_MANAGER: Failed to fetch or parse configuration content for {repo_name}. It will not be included in the cache.")
                    else:
                        logging.info(f"CONFIG_MANAGER: Using existing cached configuration for {repo_name} (SHA matches).")
                        new_cache[repo_name] = self.cached_config[repo_name]

                # Atomically update the cache
                self.cached_config = new_cache
                logging.info(f"CONFIG_MANAGER: Cache refresh finished. Current cached repo count: {len(self.cached_config)}")

            except Exception as e:
                logging.error(f"CONFIG_MANAGER: Error refreshing cache: {str(e)}", exc_info=True)

    def get_configured_repo_count(self) -> int:
        """Returns the number of repositories currently held in the config cache."""
        return len(self.cached_config)

    # --- Keep fetch_configuration_from_repository (uses REST, simpler for single file content) ---
    # Or optionally rewrite using GraphQL if preferred, but REST is fine here.
    def fetch_configuration_from_repository(self, repo_name: str, default_branch: str) -> Optional[Dict[str, Any]]:
        """
        Fetches the configuration from the repository (using REST API) and returns the parsed config dictionary or None on error.
        """
        config_file_path = self.file_path
        # Use requests with the same token for consistency
        rest_url = f"https://api.github.com/repos/{self.github_organization}/{repo_name}/contents/{config_file_path}"
        rest_headers = {
            "Authorization": f"bearer {self.github_token}",
            "Accept": "application/vnd.github.v3+json", # Standard REST header
        }
        params = {"ref": default_branch}

        try:
            response = requests.get(rest_url, headers=rest_headers, params=params, timeout=15)
            response.raise_for_status()
            file_data = response.json()

            if "content" not in file_data:
                 logger.error(f"CONFIG_MANAGER: REST API response missing 'content' for {repo_name}/{config_file_path}")
                 return None

            # Ensure content is bytes before decoding
            content_bytes = base64.b64decode(file_data['content'])
            decoded_content = content_bytes.decode('utf-8')
            config = yaml.safe_load(decoded_content)
            logging.debug(f"CONFIG_MANAGER: Fetched config content for {repo_name} via REST.")

            if not isinstance(config, dict):
                logging.error(f"CONFIG_MANAGER: Invalid configuration format in {repo_name}/{config_file_path}: expected dictionary, got {type(config)}")
                return None
            return config

        except requests.exceptions.HTTPError as e:
            if e.response.status_code == 404:
                logging.warning(f"CONFIG_MANAGER: REST API: Configuration file not found: {repo_name}/{config_file_path}")
            else:
                logging.error(f"CONFIG_MANAGER: REST API error fetching config from {repo_name}/{config_file_path}: Status {e.response.status_code}, Response: {e.response.text}")
            return None
        except requests.exceptions.RequestException as e:
             logging.error(f"CONFIG_MANAGER: Network error fetching config content via REST for {repo_name}: {e}")
             return None
        except yaml.YAMLError as e:
            logging.error(f"CONFIG_MANAGER: YAML parsing error in {repo_name}/{config_file_path}: {str(e)}")
            return None
        except Exception as e:
            logging.error(f"CONFIG_MANAGER: Unexpected error fetching configuration content from {repo_name}/{config_file_path}: {str(e)}")
            return None

    def get_repo_config(self, repo_name: str) -> Optional[RepoConfig]:
        """
        Returns the cached config for a repository, or None if not found.
        """
        config = self.cached_config.get(repo_name)
        if config is None:
                logging.debug(f"CONFIG_MANAGER: Repository config not found in cache for: {repo_name}")
        return config

    def get_all_repo_configs(self) -> Dict[str, RepoConfig]:
        """
        Returns all cached repository configurations.
        """
        return self.cached_config.values()
