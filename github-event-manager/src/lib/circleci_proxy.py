import base64
import logging
import http.client
import json

class CircleCIProxy:
    CIRCLECI_API_URL = "https://circleci.com/api/v2/project/gh/{owner}/{{repo}}/pipeline"  # CircleCI API endpoint, using {repo} to allow for dynamic repo name

    def __init__(self, repo_owner: str, circleci_token: str) -> None:
        self.repo_owner: str = repo_owner
        self.circleci_token: str = circleci_token
        self.headers = {
            "Content-Type": "application/json",
            "Circle-Token": self.circleci_token
        }
        self.circleci_api_url = CircleCIProxy.CIRCLECI_API_URL.format(owner=self.repo_owner)

    def trigger_circleci_workflow(self, repo_name: str, branch: str, event_type: str, event_action: str, payload_body: dict, parameters: dict = None, source_repository_name: str = None):
        """Triggers a CircleCI workflow for the specified repository."""
        url = self.circleci_api_url.format(repo=repo_name)
        conn = http.client.HTTPSConnection("circleci.com")
        payload_body_base64 = base64.b64encode(json.dumps(payload_body).encode('utf-8')).decode('utf-8')

        # Construct the payload with the additional parameters
        payload = {
            "branch": branch,
            "parameters": {}
        }

        if parameters:
            #to simplify the parameters we pass only strings
            payload["parameters"].update({key: str(value) for key, value in parameters.items()})
        
        # we make sure that the main parameters are always added and not overwritten by the parameters from the event-to-circleci-parameters-mappings
        payload["parameters"].update({
            "github-event-type": event_type,
            "github-event-action": event_action,
            "github-event-base64": payload_body_base64,
        })

    

        logging.info(f"CIRCLECI_PROXY: Triggering CircleCI workflow on project {repo_name} on branch {branch} parameters: {payload} in response to {event_type} {event_action} coming from {source_repository_name}")
        conn.request("POST", url, json.dumps(payload), self.headers)
        response = conn.getresponse()
        return response.status, response.reason
