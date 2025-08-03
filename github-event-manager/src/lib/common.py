import json
import os
import yaml
from typing import Dict, Any, TypedDict

class GithubEventRequest:
    """
    GithubEventRequest is a class that represents a Github event request.
    """
    def __init__(self, event_name: str,action_name: str, payload_body: bytes, json_payload: dict) -> None:
        self.event_name: str = event_name
        self.action_name: str = action_name
        self.payload_body: bytes = payload_body
        self.json_payload: dict = json_payload
        self.repository_name: str = json_payload.get("repository", {}).get("name", "")
    
    def __str__(self):
        return f"GithubEventRequest(event_name={self.event_name}, action_name={self.action_name}, repository_name={self.repository_name}, payload_body={self.payload_body}, json_payload={self.json_payload})"

class RepoMetadata(TypedDict):
    repo_name: str
    repo_default_branch: str
    repo_owner: str
    repo_owner_type: str # Will be 'Organization' based on query
    file_path: str # Will be the static path we search for
    file_sha: str
    file_size: int

class RepoConfig(TypedDict):
    """
    Represents a repository configuration.
    """
    repo_metadata: RepoMetadata
    last_fetched: int
    configuration: Dict[str, Any]

def generate_test_data(data:Dict,file_name:str, overwrite:bool=False, generate_data:bool=False):
    """
    Generate test data for a given data dictionary and file name.
    """
    if not generate_data:
        return 
    #get current file folder
    file_path = os.path.join(os.path.dirname(os.path.abspath(__file__)),"tests","test_data",file_name)
    if os.path.exists(file_path) and not overwrite:
        return 
    
    with open(file_path, 'w') as f:
        if file_name.endswith(".json"):
            json.dump(data, f, indent=4)
        elif file_name.endswith(".yml") or file_name.endswith(".yaml"):
            yaml.dump(data, f, indent=4)
    return data

def generate_test_data_bytes(data: bytes,file_name:str, overwrite:bool=False, generate_data:bool=False):
    """
    Generate test data for a given data dictionary and file name.
    """
    #we save the bytes as a string
    dict_data = { "data": data.decode('utf-8') }
    generate_test_data(dict_data, file_name, overwrite, generate_data)


def get_test_data(file_name:str)->Dict:
    """
    Get test data for a given file name.
    """
    file_path = os.path.join(os.path.dirname(os.path.abspath(__file__)),"tests","test_data",file_name)
    with open(file_path, 'r') as f:
        if file_name.endswith(".json"):
            return json.load(f)
        elif file_name.endswith(".yml") or file_name.endswith(".yaml"):
            return yaml.safe_load(f)
        else:
            return None
    
def get_test_data_bytes(file_name:str)->bytes:
    """
    Get test data for a given file name.
    """
    data = get_test_data(file_name)
    return data["data"].encode('utf-8')