import unittest
from unittest.mock import MagicMock
import json
from pprint import pprint

from ..repo_config import BaseRepoConfig, RepoConfigV1, RepoConfigV2
from ..common import GithubEventRequest, get_test_data,get_test_data_bytes


class TestBaseRepoConfig(unittest.TestCase):
    def test_create_from_dict_v1(self):
        """Test creating v1 config from dictionary"""
        config_dict = get_test_data("testing_actions_v1_github-event-handler.yml")
        
        config = BaseRepoConfig.create_from_dict("testing_actions", config_dict)
        
        self.assertIsInstance(config, RepoConfigV1)
        self.assertEqual(config.version, 1)
        self.assertEqual(config.source_repository_name, "testing_actions")
        self.assertIn("pull_request", config.listen_for_events)
    
    def test_create_from_dict_v2(self):
        """Test creating v2 config from dictionary"""
        config_dict = get_test_data("testing_actions_v2_github-event-handler.yml")
        
        config = BaseRepoConfig.create_from_dict("testing_actions", config_dict)
        
        self.assertIsInstance(config, RepoConfigV2)
        self.assertEqual(config.version, 2)
        self.assertEqual(config.source_repository_name, "testing_actions")
        self.assertIn("pull_request", config.listen_for_events)
    
    def test_create_from_dict_invalid_version(self):
        """Test with invalid version number"""
        config_dict = {"version": 99}
        config = BaseRepoConfig.create_from_dict("test-repo", config_dict)
        self.assertIsNone(config)
    
    def test_create_from_dict_invalid_input(self):
        """Test with invalid input type"""
        config = BaseRepoConfig.create_from_dict("test-repo", "not-a-dict")
        self.assertIsNone(config)
    
    def test_extract_value_from_path(self):
        """Test extracting values using dot notation paths"""
        data_bytes = get_test_data("testing_actions_pull_request_labeled_payload_body.json")["data"]
        data = json.loads(data_bytes)
        # Test valid paths
        self.assertEqual(
            BaseRepoConfig.extract_value_from_path(data, ".pull_request.number"), 
            data["pull_request"]["number"]
        )
        self.assertEqual(
            BaseRepoConfig.extract_value_from_path(data, ".pull_request.labels.0.name"), 
            data["pull_request"]["labels"][0]["name"]
        )
        
        # Test invalid paths
        self.assertIsNone(
            BaseRepoConfig.extract_value_from_path(data, ".issue.number")
        )


class TestRepoConfigV1(unittest.TestCase):
    def setUp(self):

        self.config_dict = get_test_data("testing_actions_v1_github-event-handler.yml")        
        self.config = RepoConfigV1.from_dict("testing_actions", self.config_dict)
        
    
    def test_from_dict(self):
        """Test creating RepoConfigV1 from dictionary"""
        self.assertIsInstance(self.config, RepoConfigV1)
        self.assertEqual(self.config.version, 1)
        self.assertEqual(self.config.source_repository_name, "testing_actions")
        self.assertIn("pull_request", self.config.listen_for_events)
        self.assertNotIn("issues", self.config.listen_for_events)
    
    def test_from_dict_invalid(self):
        """Test with invalid input"""
        # Wrong version
        config_dict = {
            "version": 2,
            "listen-for-events": {}
        }
        config = RepoConfigV1.from_dict("test-repo", config_dict)
        self.assertIsNone(config)
        
        # Not a dict
        config = RepoConfigV1.from_dict("test-repo", "not-a-dict")
        self.assertIsNone(config)
    
    def test_is_event_allowed(self):
        """Test checking if an event is allowed"""
        # Create a sample event
        allowed_event = GithubEventRequest(
            event_name="pull_request",
            action_name="labeled",
            payload_body=get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json").decode('utf-8'))
        )
        
        # Test allowed event
        self.assertTrue(self.config.is_event_allowed(allowed_event))
        
        # Test disallowed event - wrong event name
        disallowed_event1 = GithubEventRequest(
            event_name="workflow_run",
            action_name="completed",
            payload_body=get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json").decode('utf-8'))
        )
        self.assertFalse(self.config.is_event_allowed(disallowed_event1))
        
        # Test disallowed event - wrong action
        disallowed_event2 = GithubEventRequest(
            event_name="pull_request",
            action_name="closed",
            payload_body=get_test_data_bytes("testing_actions_push__payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_push__payload_body.json").decode('utf-8'))
        )
        self.assertFalse(self.config.is_event_allowed(disallowed_event2))
    
    def test_get_parameter_mappings(self):
        """Test extracting parameters from event payload"""
        # Create a sample event
        event = GithubEventRequest(
            event_name="pull_request",
            action_name="labeled",
            payload_body=get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json").decode('utf-8'))
        )
        
        print(self.config.get_parameter_mappings(event))
        github_event_mappings,circleci_parameters = self.config.get_parameter_mappings(event)
        print(f"github_event_mappings: {github_event_mappings}")
        print(f"circleci_parameters: {circleci_parameters}")
        
        self.assertEqual(github_event_mappings["pull_request_number"], 58)
        self.assertEqual(github_event_mappings["label_name"], "bug")


class TestRepoConfigV2(unittest.TestCase):
    def setUp(self):
        """Set up test fixtures"""
        self.config_dict_1 = get_test_data("testing_actions_v2_github-event-handler.yml")
        self.config_1 = RepoConfigV2.from_dict("testing_actions", self.config_dict_1)
        self.config_dict_2 = get_test_data("testing_actions_v2_github-event-handler2.yml")
        self.config_2 = RepoConfigV2.from_dict("testing_actions", self.config_dict_2)
        self.config_dict_3 = get_test_data("testing_actions_v2_github-event-handler3.yml")
        self.config_3 = RepoConfigV2.from_dict("testing_actions", self.config_dict_3)
        
  
    def test_from_dict_1(self):
        """Test creating RepoConfigV2 from dictionary"""
        self.assertIsInstance(self.config_1, RepoConfigV2)
        self.assertEqual(self.config_1.version, 2)
        self.assertEqual(self.config_1.source_repository_name, "testing_actions")
        self.assertIn("pull_request", self.config_1.listen_for_events)
        self.assertEqual(self.config_1.listen_for_events["pull_request"]["types"], ["opened", "synchronize", "reopened"])
        self.assertEqual(self.config_1.listen_for_events["pull_request"]["filters"], {"number": 58})
        self.assertEqual(self.config_1.listen_for_events["pull_request"]["event-to-github-event-mappings"], [{"pull_request_number": ".pull_request.number"},{"pull_request_state": ".pull_request.state"}])
        self.assertEqual(self.config_1.listen_for_events["pull_request"]["event-to-circleci-parameters-mappings"], [{"pull_request_number": ".pull_request.number"},{"pull_request_state": ".pull_request.state"}])
        
    def test_from_dict_2(self):
        """Test creating RepoConfigV2 from dictionary"""
        self.assertIsInstance(self.config_2, RepoConfigV2)
        self.assertEqual(self.config_2.version, 2)
        self.assertEqual(self.config_2.source_repository_name, "testing_actions")
        self.assertIn("pull_request", self.config_2.listen_for_events)
        self.assertEqual(self.config_2.listen_for_events["pull_request"]["types"], ["opened", "synchronize", "reopened"])
        self.assertEqual(self.config_2.listen_for_events["pull_request"]["event-to-github-event-mappings"], [{"pull_request_number": ".pull_request.number"},{"pull_request_state": ".pull_request.state"}])
        self.assertEqual(self.config_2.listen_for_events["pull_request_review"]["event-to-circleci-parameters-mappings"], [{"pull_request_number": ".pull_request.number"},{"repository": ".repository.name"}])
        
      
    def test_is_event_allowed_with_source_repo_1(self):
        """Test checking if an event is allowed based on source repos"""
        # Create a sample event from an allowed repo
        allowed_event = GithubEventRequest(
            event_name="pull_request",
            action_name="synchronize",
            payload_body=get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json").decode('utf-8'))
        )
    
        # Test event from allowed repo
        self.assertTrue(self.config_1.is_event_allowed(allowed_event))

        allowed_event = GithubEventRequest(
            event_name="pull_request_review",
            action_name="submitted",
            payload_body=get_test_data_bytes("testing_actions_pull_request_review_submitted_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_review_submitted_body.json").decode('utf-8'))
        )
    
        # Test event from allowed repo
        self.assertTrue(self.config_2.is_event_allowed(allowed_event))
        
        # Test event from disallowed repo
        disallowed_event = GithubEventRequest(
            event_name="pull_request",
            action_name="labeled",
            payload_body=get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json").decode('utf-8'))
        )
        self.assertFalse(self.config_1.is_event_allowed(disallowed_event))

        


        disallowed_event_2 = GithubEventRequest(
            event_name="pull_request",
            action_name="synchronize",
            payload_body=get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_synchronize_payload_body.json").decode('utf-8'))
        )
    
        # Test event from allowed repo
        self.assertTrue(self.config_3.is_event_allowed(disallowed_event_2))
     
    def test_get_parameter_mappings(self):
        """Test extracting parameters from event payload"""
        # Create a sample event
        event = GithubEventRequest(
            event_name="pull_request",
            action_name="labeled",
            payload_body=get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json"),
            json_payload=json.loads(get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json").decode('utf-8'))
        )
        
        github_event_mappings,circleci_parameters = self.config_1.get_parameter_mappings(event)
        
        self.assertEqual(github_event_mappings["pull_request_number"], 58)
        self.assertEqual(github_event_mappings["pull_request_state"], "open")

        self.assertEqual(circleci_parameters["pull_request_number"], 58)
        self.assertEqual(circleci_parameters["pull_request_state"], "open")



if __name__ == '__main__':
    unittest.main()
