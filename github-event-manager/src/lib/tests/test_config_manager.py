import unittest
from unittest.mock import patch, MagicMock
import json
import base64

from ..config_manager import ConfigManager
from ..common import RepoMetadata, RepoConfig, get_test_data


class TestConfigManager(unittest.TestCase):
    def setUp(self):
        # Mock the repo_search_counter for testing
        self.mock_counter = MagicMock()
        self.mock_counter.inc = MagicMock()
        
        # Create a ConfigManager instance for tests
        self.config_manager = ConfigManager(
            github_organization="ethereum-optimism",
            github_token="fake-token",
            repo_search_counter=self.mock_counter
        )
    
    @patch('requests.post')
    def test_execute_graphql_query(self, mock_post):
        # Setup mock response
        mock_response = MagicMock()
        mock_response.json.return_value = get_test_data("graphql_query_response.json")
        mock_post.return_value = mock_response
        
        # Test the method
        query = ConfigManager.QUERY_REPOSITORIES
        variables = {"org": self.config_manager.github_organization, "cursor": None}
        result = self.config_manager._execute_graphql_query(query, variables)
        
        # Assertions
        mock_post.assert_called_once()
        self.assertEqual(result, get_test_data("graphql_query_response.json")["data"])
    
    @patch('requests.post')
    def test_execute_graphql_query_error(self, mock_post):
        # Setup mock response with error
        mock_response = MagicMock()
        mock_response.json.return_value = {"errors": [{"message": "Error"}]}
        mock_post.return_value = mock_response
        
        # Test the method
        result = self.config_manager._execute_graphql_query("query", {})
        
        # Assertions
        mock_post.assert_called_once()
        self.assertIsNone(result)
    
    @patch.object(ConfigManager, '_get_all_organization_repos')
    @patch.object(ConfigManager, '_check_files_in_batch')
    def test_search_all_repos(self, mock_check_files, mock_get_repos):
        # Setup mocks
        mock_get_repos.return_value = get_test_data("_get_all_organization_repos_result.json")
        result1 = get_test_data("_check_files_in_batch_result_1.json")
        result2 = get_test_data("_check_files_in_batch_result_2.json")
        result3 = get_test_data("_check_files_in_batch_result_3.json")
        result4 = get_test_data("_check_files_in_batch_result_4.json")
        result5 = get_test_data("_check_files_in_batch_result_5.json")
        
        # Configure the mock to return different values on consecutive calls
        mock_check_files.side_effect = [result1, result2, result3, result4, result5]
        
        # Test the method
        result = self.config_manager.search_all_repos()
        # Assertions
        mock_get_repos.assert_called_once()
        self.assertEqual(mock_check_files.call_count, 5)

        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["repo_name"], "optimism")
        self.assertEqual(result[1]["repo_name"], "superchain-ops")
    
    @patch('requests.get')
    def test_fetch_configuration_from_repository(self, mock_get):
        # Setup mock response
        mock_response = MagicMock()
        yaml_content = "version: 1\nlisten-for-events:\n  pull_request:\n    types: [labeled]"
        encoded_content = base64.b64encode(yaml_content.encode('utf-8')).decode('utf-8')
        mock_response.json.return_value = {"content": encoded_content}
        mock_get.return_value = mock_response
        
        # Test the method
        result = self.config_manager.fetch_configuration_from_repository(
            repo_name="repo1",
            default_branch="main"
        )
        
        # Assertions
        mock_get.assert_called_once()
        self.assertEqual(result["version"], 1)
        self.assertIn("listen-for-events", result)
        self.assertIn("pull_request", result["listen-for-events"])
    
    @patch.object(ConfigManager, 'search_all_repos')
    @patch.object(ConfigManager, 'fetch_configuration_from_repository')
    def test_refresh_cache(self, mock_fetch, mock_search):
        # Setup mocks
        repo_metadata = RepoMetadata(
            repo_name="repo1",
            repo_default_branch="main",
            repo_owner="test-org",
            repo_owner_type="Organization",
            file_path=".circleci/github-event-handler.yml",
            file_sha="abc123",
            file_size=100
        )
        mock_search.return_value = [repo_metadata]
        
        config_data = {
            "version": 1,
            "listen-for-events": {
                "pull_request": {
                    "types": ["labeled"]
                }
            }
        }
        mock_fetch.return_value = config_data
        
        # Patch the BaseRepoConfig.create_from_dict method
        with patch('lib.repo_config.BaseRepoConfig.create_from_dict') as mock_create:
            mock_create.return_value = {"version": 1}
            
            # Test the method
            self.config_manager.refresh_cache()
            
            # Assertions
            mock_search.assert_called_once()
            mock_fetch.assert_called_once()
            mock_create.assert_called_once()
            self.mock_counter.inc.assert_called_once()
            
            # Check cache contents
            self.assertEqual(self.config_manager.get_configured_repo_count(), 1)
            config = self.config_manager.get_repo_config("repo1")
            self.assertIsNotNone(config)
            self.assertEqual(config["repo_metadata"], repo_metadata)

  

if __name__ == '__main__':
    unittest.main()
