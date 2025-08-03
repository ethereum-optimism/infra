import unittest
from unittest.mock import patch, MagicMock, call
import json
import base64
import hmac
import hashlib

from ..event_manager import EventManager
from ..common import GithubEventRequest, RepoMetadata, get_test_data_bytes, get_test_data
from ..repo_config import BaseRepoConfig


class TestEventManager(unittest.TestCase):
    def setUp(self):
        # Mock dependencies
        self.mock_config_manager = MagicMock()
        self.mock_sent_counter = MagicMock()
        self.mock_succeeded_counter = MagicMock()
        self.mock_events_received_counter = MagicMock()
        
        # Create EventManager instance for testing
        self.event_manager = EventManager(
            valid_organization="ethereum-optimism",
            github_webhook_secret="your_webhook_secret",
            circleci_token="your_circleci_token",
            config_manager=self.mock_config_manager,
            triggers_sent_counter=self.mock_sent_counter,
            triggers_succeeded_counter=self.mock_succeeded_counter,
            events_received_counter=self.mock_events_received_counter
        )
        
        # Mock CircleCI proxy
        self.event_manager.circleci_proxy = MagicMock()
        
        # Sample payload for tests
        self.sample_payload = {
            "action": "labeled",
            "repository": {"name": "test-repo"},
            "enterprise": {"name": "test-org"},
            "organization": {"login": "test-org"},
            "sender": {"login": "test-user"},
            "label": {"name": "bug"}
        }
        

        
    
    def test_is_signature_valid(self):
        # Test valid signature
        payload = get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json")
        original_header = get_test_data_bytes("testing_actions_pull_request_labeled_signature_header.json").decode('utf-8')
        secret = "your_webhook_secret"
        invalid_secret = "invalid_secret"
        
        # Create signature same way GitHub would
        h = hmac.new(secret.encode('utf-8'), msg=payload, digestmod=hashlib.sha256)
        valid_signature = f"sha256={h.hexdigest()}"

        self.assertEqual(original_header, valid_signature)
        
        # Test validation
        self.assertTrue(self.event_manager.is_signature_valid(payload, valid_signature))

        # Create signature same way GitHub would
        h = hmac.new(invalid_secret.encode('utf-8'), msg=payload, digestmod=hashlib.sha256)
        invalid_signature = f"sha256={h.hexdigest()}"
        
        # Test invalid signature
        invalid_signature = "sha256=invalid_hash"
        self.assertFalse(self.event_manager.is_signature_valid(payload, invalid_signature))
        
        # Test empty signature
        self.assertFalse(self.event_manager.is_signature_valid(payload, ""))


    
    def test_validate_request_valid(self):
        # Create mock request
        mock_request = MagicMock()
        
        # Sample payload
        payload = get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json")

        
        # Create valid signature
        h = hmac.new(b"test-secret", msg=payload, digestmod=hashlib.sha256)
        valid_signature = f"sha256={h.hexdigest()}"
        
        # Setup mock request
        mock_request.headers = {
            "X-Hub-Signature-256": valid_signature,
            "Content-Type": "application/json",
            "X-GitHub-Event": "pull_request"
        }
        mock_request.get_data.return_value = payload
        
        # Test valid request
        with patch.object(EventManager, 'is_signature_valid', return_value=True):
            result = self.event_manager.validate_request(mock_request)
        
        # Assertions
        self.assertIsNotNone(result)
        self.assertEqual(result.event_name, "pull_request")
        self.assertEqual(result.action_name, "labeled")
        self.assertEqual(result.repository_name, "testing_actions")

    def test_validate_request_valid_2(self):
        # Create mock request
        mock_request = MagicMock()
        
        # Sample payload
        payload = get_test_data_bytes("testing_actions_pull_request_review_submitted_body.json")

        
        # Create valid signature
        h = hmac.new(b"test-secret", msg=payload, digestmod=hashlib.sha256)
        valid_signature = f"sha256={h.hexdigest()}"
        
        # Setup mock request
        mock_request.headers = {
            "X-Hub-Signature-256": valid_signature,
            "Content-Type": "application/json",
            "X-GitHub-Event": "pull_request_review"
        }
        mock_request.get_data.return_value = payload
        
        # Test valid request
        with patch.object(EventManager, 'is_signature_valid', return_value=True):
            result = self.event_manager.validate_request(mock_request)
        
        # Assertions
        self.assertIsNotNone(result)
        self.assertEqual(result.event_name, "pull_request_review")
        self.assertEqual(result.action_name, "submitted")
        self.assertEqual(result.repository_name, "testing_actions")
    
    def test_validate_request_invalid_signature(self):
        # Create mock request
        mock_request = MagicMock()
        
        # Setup mock request
        mock_request.headers = {
            "X-Hub-Signature-256": "invalid-signature",
            "Content-Type": "application/json",
            "X-GitHub-Event": "pull_request"
        }
        mock_request.get_data.return_value = get_test_data_bytes("testing_actions_pull_request_labeled_payload_body.json")
        
        # Mock signature validation to return False
        with patch.object(EventManager, 'is_signature_valid', return_value=False):
            result = self.event_manager.validate_request(mock_request)
        
        # Should return None for invalid signature
        self.assertIsNone(result)
    
    # def test_process_event(self):
    #     # Create the mock configuration object differently
    #     mock_config = MagicMock()
    #     # Explicitly add the methods we'll be calling
    #     mock_config.is_event_allowed = MagicMock(return_value=True)
    #     mock_config.get_parameter_mappings = MagicMock(return_value={"pr_number": 123})
        
    #     # Create the mock metadata object
    #     mock_metadata = MagicMock()
    #     mock_metadata.repo_name = "test-repo"
    #     # For dictionary access
    #     mock_metadata.__getitem__ = MagicMock(side_effect=lambda key: "main" if key == "repo_default_branch" else None)
        
    #     # Assemble the repo config dict
    #     mock_repo_config = {
    #         "configuration": mock_config,
    #         "repo_metadata": mock_metadata
    #     }
        
    #     # Setup repo_configs to return our mock
    #     self.mock_config_manager.get_all_repo_configs.return_value = [mock_repo_config]
        
    #     # Setup CircleCI response
    #     self.event_manager.circleci_proxy.trigger_circleci_workflow.return_value = (200, "OK")
        
    #     # Run the method
    #     self.event_manager.process_event(self.test_event)
        
    #     # Verify interactions
    #     mock_repo_config["configuration"].is_event_allowed.assert_called_once_with(self.test_event)
    #     mock_repo_config["configuration"].get_parameter_mappings.assert_called_once_with(self.test_event)
    #     self.event_manager.circleci_proxy.trigger_circleci_workflow.assert_called_once()
    #     self.mock_sent_counter.labels.assert_called_once_with(repository="test-repo")
    #     self.mock_succeeded_counter.labels.assert_called_once_with(repository="test-repo")
    
    # def test_process_event_no_matching_configs(self):
    #     # Create the mock config properly
    #     mock_config = MagicMock()
    #     mock_config.is_event_allowed = MagicMock(return_value=False)
        
    #     mock_metadata = MagicMock()
        
    #     # Assemble the repo config dict
    #     mock_repo_config = {
    #         "configuration": mock_config,
    #         "repo_metadata": mock_metadata
    #     }
        
    #     # Setup repo_configs to return our mock
    #     self.mock_config_manager.get_all_repo_configs.return_value = [mock_repo_config]
        
    #     # Run the method
    #     self.event_manager.process_event(self.test_event)
        
    #     # CircleCI proxy should not be called
    #     self.event_manager.circleci_proxy.trigger_circleci_workflow.assert_not_called()
    #     self.mock_sent_counter.labels.assert_not_called()
    
    # def test_process_event_failed_trigger(self):
    #     # Create the mock config properly
    #     mock_config = MagicMock()
    #     mock_config.is_event_allowed = MagicMock(return_value=True)
    #     mock_config.get_parameter_mappings = MagicMock(return_value={"pr_number": 123})
        
    #     # Create the mock metadata object
    #     mock_metadata = MagicMock()
    #     mock_metadata.repo_name = "test-repo"
    #     # For dictionary access
    #     mock_metadata.__getitem__ = MagicMock(side_effect=lambda key: "main" if key == "repo_default_branch" else None)
        
    #     # Assemble the repo config dict
    #     mock_repo_config = {
    #         "configuration": mock_config,
    #         "repo_metadata": mock_metadata
    #     }
        
    #     # Setup repo_configs to return our mock
    #     self.mock_config_manager.get_all_repo_configs.return_value = [mock_repo_config]
        
    #     # Setup CircleCI response - failure
    #     self.event_manager.circleci_proxy.trigger_circleci_workflow.return_value = (500, "Server Error")
        
    #     # Run the method
    #     self.event_manager.process_event(self.test_event)
        
    #     # Verify sent counter is called but success counter is not
    #     self.mock_sent_counter.labels().inc.assert_called_once()
    #     self.mock_succeeded_counter.labels().inc.assert_not_called()


if __name__ == '__main__':
    unittest.main()
