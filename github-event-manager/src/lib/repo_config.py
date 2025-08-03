from typing import Dict, List, Optional, Any, Union, TYPE_CHECKING, Tuple
from dataclasses import dataclass
from pprint import pprint
from .common import GithubEventRequest


@dataclass
class BaseRepoConfig:
    """Base class for repository configuration."""
    version: int
    source_repository_name: str
    
    @staticmethod
    def create_from_dict(source_repo_name: str, config_dict: Dict[str, Any]) -> Optional['BaseRepoConfig']:
        """Factory method to create the appropriate config version."""
        if not isinstance(config_dict, dict):
            return None
            
        version = config_dict.get('version')
        if version == 1:
            return RepoConfigV1.from_dict(source_repo_name, config_dict)
        elif version == 2:
            return RepoConfigV2.from_dict(source_repo_name, config_dict)
        else:
            # Unknown version
            return None
        
    @staticmethod
    def extract_value_from_path(data, path):
        """Extract nested value using dot notation (e.g., .issue.number)"""
        keys = path.lstrip('.').split('.')
        for key in keys:
            if isinstance(data, dict) and key in data:
                data = data[key]
            elif isinstance(data, list) and key.isdigit() and int(key) < len(data):
                # Handle array indexing
                data = data[int(key)]
            else:
                return None
        return data


@dataclass
class RepoConfigV1(BaseRepoConfig):
    """
    Version 1 of repository configuration.
    
    Example:
    ```yaml
    version: 1
    listen-for-events:
      pull_request:
        types: [labeled]
        event-to-parameters-mappings:
          - pull_request_number: .pull_request.number
          - label_name: .label.name
    ```
    """
    listen_for_events: Dict[str, Dict[str, Any]]
    
    @classmethod
    def from_dict(cls, source_repo_name: str, config_dict: Dict[str, Any]) -> Optional['RepoConfigV1']:
        """Create a RepoConfigV1 from a dictionary."""
        if not isinstance(config_dict, dict):
            return None
            
        version = config_dict.get('version')
        if version != 1:
            return None
            
        listen_for_events = config_dict.get('listen-for-events', {})
        
        return cls(
            version=version,
            listen_for_events=listen_for_events,
            source_repository_name=source_repo_name
        )
    
    def is_event_allowed(self, github_event: "GithubEventRequest") -> bool:
        """Check if an event is allowed."""
        # we need to check if the event name is in the listen_for_events dict
        if github_event.event_name not in self.listen_for_events:
            return False
        # we need to check if the event action is in the listen_for_events dict
        if github_event.action_name not in self.listen_for_events[github_event.event_name]["types"]:
            return False
        return True

    def get_parameter_mappings(self, github_event: "GithubEventRequest") -> Tuple[Dict[str, str], Dict[str, str]]:
        """Get the parameter mappings for a specific event."""
        paylod_to_send = {}
        circleci_parameters = {}
        if "event-to-parameters-mappings" in self.listen_for_events[github_event.event_name]:
            event_to_parameters_mappings = self.listen_for_events[github_event.event_name]["event-to-parameters-mappings"]
            for event_to_parameter in event_to_parameters_mappings:
                [(param_name, json_path)] = event_to_parameter.items()
                paylod_to_send[param_name] = self.extract_value_from_path(github_event.json_payload, json_path)
        circleci_parameters = None
        return (paylod_to_send, circleci_parameters)


@dataclass
class RepoConfigV2(BaseRepoConfig):
    """
    Version 2 of repository configuration.
    
    Example:
    ```yaml
    version: 2
    listen-for-events:
      pull_request:
        source_repositories: [optimism, testing_actions]  # if omitted, defaults to current repository
        types: [labeled]
        filters:
          label.name: "bug"  
        event-to-github-event-mappings:
          - pull_request_number: .pull_request.number
          - label_name: .label.name
        event-to-circleci-parameters-mappings:
          - circleci_parameter_name: .circleci_parameter_value
    ```    """
    listen_for_events: Dict[str, Dict[str, Any]]
    
    @classmethod
    def from_dict(cls, source_repo_name: str, config_dict: Dict[str, Any]) -> Optional['RepoConfigV2']:
        """Create a RepoConfigV2 from a dictionary."""
        if not isinstance(config_dict, dict):
            return None
            
        version = config_dict.get('version')
        if version != 2:
            return None
            
        listen_for_events = config_dict.get('listen-for-events', {})
        
        return cls(
            version=version,
            listen_for_events=listen_for_events,
            source_repository_name=source_repo_name
        )
    
    def is_event_allowed(self, github_event: GithubEventRequest) -> bool:
        """Check if an event is allowed."""
        # we need to check if the event name is in the listen_for_events dict
        if github_event.event_name not in self.listen_for_events:
            return False
        
        # we need to check if the event action is in the listen_for_events dict
        if github_event.action_name not in self.listen_for_events[github_event.event_name]["types"]:
            return False
        
        # we need to check if the event source_repositories is in the listen_for_events dict
        if "source_repositories" in self.listen_for_events[github_event.event_name]:
            if github_event.repository_name not in self.listen_for_events[github_event.event_name]["source_repositories"]:
                return False
        else:
            # if the event source_repositories is not in the listen_for_events dict, we need to check if the event repository_name is the same as the source_repository_name
            if github_event.repository_name != self.source_repository_name:
                return False
        # we need to check if the event filters are in the listen_for_events dict
        if "filters" in self.listen_for_events[github_event.event_name]:
            for filter_key in self.listen_for_events[github_event.event_name]["filters"]:
                filter_value=self.listen_for_events[github_event.event_name]["filters"][filter_key]
                filter_value_from_event = self.extract_value_from_path(github_event.json_payload, filter_key)
                # Handle array or single value comparison
                if isinstance(filter_value, list):
                    if filter_value_from_event not in filter_value:
                        return False
                else:
                    # Direct comparison for non-array values
                    if filter_value_from_event != filter_value:
                        return False
        return True

    def get_parameter_mappings(self, github_event: "GithubEventRequest") -> Tuple[Dict[str, str], Dict[str, str]]:
        """Get the parameter mappings for a specific event."""
        paylod_to_send = {}
        if "event-to-github-event-mappings" in self.listen_for_events[github_event.event_name]:
            event_to_parameters_mappings = self.listen_for_events[github_event.event_name]["event-to-github-event-mappings"]
            for event_to_parameter in event_to_parameters_mappings:
                [(param_name, json_path)] = event_to_parameter.items()
                paylod_to_send[param_name] = self.extract_value_from_path(github_event.json_payload, json_path)

        circleci_parameters = {}
        if "event-to-circleci-parameters-mappings" in self.listen_for_events[github_event.event_name]:
            event_to_parameters_mappings = self.listen_for_events[github_event.event_name]["event-to-circleci-parameters-mappings"]
            for event_to_parameter in event_to_parameters_mappings:
                [(param_name, json_path)] = event_to_parameter.items()
                circleci_parameters[param_name] = self.extract_value_from_path(github_event.json_payload, json_path)

        return (paylod_to_send, circleci_parameters)
