# GitHub Event Manager
https://docs.github.com/en/webhooks/webhook-events-and-payloads

This project handles all GitHub webhook events. For detailed information about each event's payload structure and properties, refer to the GitHub Webhooks documentation linked above.

## Configuration File

The application uses a YAML configuration file to specify which GitHub events to listen for and how to map event data to parameters.

### Version 1
```yaml
version: 1
listen-for-events:
  pull_request:
    # https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
    types: [labeled]
    event-to-parameters-mappings:
      - pull_request_number: .pull_request.number
      - label_name: .label.name
```

#### Version 1 Structure:
- `version`: Specifies the configuration format version
- `listen-for-events`: Defines which GitHub events to process
  - Event type (e.g., `pull_request`): The GitHub webhook event to listen for
    - `types`: Array of specific action types to process (e.g., `labeled`)
    - `event-to-parameters-mappings`: Maps data from the webhook payload to named parameters
      - Each mapping extracts a value from the JSON payload using JSONPath notation

### Version 2
```yaml
version: 2
listen-for-events:
  pull_request:
    # https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues
    types: [labeled]
    source_repositories: [optimism, testing_actions]  # if omitted, defaults to current repository
    filters:
      label.name: "bug"  
    event-to-github-event-mappings: #these are passed to circleci inside the github-event-base64
      - pull_request_number: .pull_request.number
    event-to-circleci-parameters-mappings: #these are passed to circleci as parameters
      - pull_request_number: .pull_request.number
```

#### Version 2 Structure:
- `version`: Specifies the configuration format version (2)
- `listen-for-events`: Defines which GitHub events to process
  - Event type (e.g., `pull_request`): The GitHub webhook event to listen for
    - `source_repositories`: Optional array of repository names to listen for events from
    - `filters`: Conditions that must be met for the event to be processed
      - `action`: Array of specific action types to process (e.g., `labeled`)
      - Additional custom filters using dot notation to match payload fields (e.g., `label.name: "bug"`)
    - `event-to-parameters-mappings`: Maps data from the webhook payload to named parameters
      - Each mapping extracts a value from the JSON payload using JSONPath notation

## Supported Events and Actions

| Event | Actions |
|-------|---------|
| `branch_protection_rule` | `created`, `deleted`, `edited` |
| `check_run` | `created`, `completed`, `rerequested`, `requested_action` |
| `check_suite` | `completed`, `requested`, `rerequested` |
| `code_scanning_alert` | `created`, `reopened`, `closed`, `fixed`, `appeared_in_branch`, `reopened_by_user`, `closed_by_user` |
| `commit_comment` | `created` |
| `create` | *Triggered when ref is created* |
| `delete` | *Triggered when ref is deleted* |
| `deploy_key` | `created`, `deleted` |
| `deployment` | `created` |
| `deployment_status` | `created` |
| `discussion` | `created`, `edited`, `deleted`, `transferred`, `pinned`, `unpinned`, `labeled`, `unlabeled`, `locked`, `unlocked`, `category_changed`, `answered`, `unanswered` |
| `discussion_comment` | `created`, `edited`, `deleted` |
| `fork` | *Triggered when repo is forked* |
| `gollum` | *Triggered when wiki page is created/updated* |
| `installation` | `created`, `deleted`, `suspend`, `unsuspend` |
| `installation_repositories` | `added`, `removed` |
| `issue_comment` | `created`, `edited`, `deleted` |
| `issues` | `opened`, `edited`, `deleted`, `transferred`, `pinned`, `unpinned`, `closed`, `reopened`, `assigned`, `unassigned`, `labeled`, `unlabeled`, `locked`, `unlocked`, `milestoned`, `demilestoned` |
| `label` | `created`, `edited`, `deleted` |
| `marketplace_purchase` | `purchased`, `cancelled`, `pending_change`, `pending_change_cancelled`, `changed` |
| `member` | `added`, `removed`, `edited` |
| `membership` | `added`, `removed` |
| `meta` | `deleted` |
| `milestone` | `created`, `closed`, `opened`, `edited`, `deleted` |
| `organization` | `deleted`, `renamed`, `member_added`, `member_removed`, `member_invited` |
| `org_block` | `blocked`, `unblocked` |
| `package` | `published`, `updated` |
| `page_build` | *Triggered when Pages build completes* |
| `ping` | *Triggered when webhook is created* |
| `project` | `created`, `edited`, `closed`, `reopened`, `deleted` |
| `project_card` | `created`, `edited`, `moved`, `converted`, `deleted` |
| `project_column` | `created`, `edited`, `moved`, `deleted` |
| `public` | *Triggered when repo is made public* |
| `pull_request` | `assigned`, `auto_merge_disabled`, `auto_merge_enabled`, `closed`, `converted_to_draft`, `edited`, `labeled`, `locked`, `opened`, `ready_for_review`, `reopened`, `review_request_removed`, `review_requested`, `synchronized`, `unassigned`, `unlabeled`, `unlocked` |
| `pull_request_review` | `submitted`, `edited`, `dismissed` |
| `pull_request_review_comment` | `created`, `edited`, `deleted` |
| `pull_request_review_thread` | `resolved`, `unresolved` |
| `push` | *Triggered when commits are pushed* |
| `release` | `published`, `unpublished`, `created`, `edited`, `deleted`, `prereleased`, `released` |
| `repository` | `created`, `deleted`, `archived`, `unarchived`, `edited`, `renamed`, `transferred`, `publicized`, `privatized` |
| `repository_dispatch` | *Custom client-defined actions* |
| `repository_import` | `success`, `cancelled`, `failure` |
| `repository_vulnerability_alert` | `create`, `dismiss`, `resolve` |
| `secret_scanning_alert` | `created`, `resolved`, `reopened` |
| `security_advisory` | `published`, `updated`, `withdrawn` |
| `sponsorship` | `created`, `cancelled`, `edited`, `pending_cancellation`, `pending_tier_change`, `tier_changed` |
| `star` | `created`, `deleted` |
| `status` | *Triggered when status is updated* |
| `team` | `created`, `deleted`, `edited`, `added_to_repository`, `removed_from_repository` |
| `team_add` | *Triggered when repo is added to team* |
| `watch` | `started` |
| `workflow_dispatch` | *Triggered when workflow is manually triggered* |
| `workflow_job` | `queued`, `in_progress`, `completed` |
| `workflow_run` | `requested`, `completed` |

## How to Use GitHub Event Manager

Setting up GitHub Event Manager requires configuration in both the GitHub repository and the CircleCI project.

### Step 1: Create the GitHub Event Handler Configuration

Create a file named `.circleci/github-event-handler.yml` in your repository with either a version 1 or version 2 configuration:

```yaml
version: 2
listen-for-events:
  pull_request:
    types: [labeled]
    event-to-parameters-mappings:
      - pull_request_number: .pull_request.number
      - label_name: .label.name
    event-to-github-event-mappings:
      - pr_data: .pull_request
    event-to-circleci-parameters-mappings:
      - pull_request_number: .pull_request.number
```

### Step 2: Configure CircleCI to Accept Webhook Triggers

Modify your `.circleci/config.yml` to handle the parameters that GitHub Event Manager will send:

```yaml
version: 2.1

parameters:
  # Default parameters provided by GitHub Event Manager
  github-event-type:  # Will contain the event type (e.g., "pull_request")
    type: string
    default: "__not_set__"
  github-event-action:  # Will contain the action (e.g., "labeled")
    type: string
    default: "__not_set__"
  github-event-base64:  # Will contain encoded data from event-to-github-event-mappings
    type: string
    default: "__not_set__"
    
  # Custom parameters from event-to-circleci-parameters-mappings
  pull_request_number:  # Example custom parameter
    type: integer
    default: 0

jobs:
  process_labeled_pr:
    docker:
      - image: cimg/base:current
    steps:
      - checkout
      - run:
          name: Process PR Labels
          command: |
            echo "Processing PR #<< pipeline.parameters.pull_request_number >>"
            
            # Decode the base64 payload if needed
            if [ "<< pipeline.parameters.github-event-base64 >>" != "__not_set__" ]; then
              echo "<< pipeline.parameters.github-event-base64 >>" | base64 -d > event_data.json
              # Process the decoded JSON data
              cat event_data.json
            fi
            
            # Continue with your workflow...

workflows:
  version: 2
  
  # This workflow runs when triggered by GitHub Event Manager for PR labeling
  handle_pr_label:
    when:
      and:
        - equal: [<< pipeline.parameters.github-event-type >>, "pull_request"]
        - equal: [<< pipeline.parameters.github-event-action >>, "labeled"]
    jobs:
      - process_labeled_pr
      
  # Your other workflows...
```

### Step 3: Event Flow and Parameter Handling

When a GitHub webhook event is received:

1. GitHub Event Manager checks if the event matches any configurations in `.circleci/github-event-handler.yml`
2. If matched, it extracts parameters using the defined mappings:
   - `event-to-parameters-mappings`: For internal use within the manager
   - `event-to-github-event-mappings`: Values encoded and sent as `github-event-base64`
   - `event-to-circleci-parameters-mappings`: Values sent as direct CircleCI parameters

3. GitHub Event Manager triggers the CircleCI pipeline with:
   - Standard parameters (`github-event-type`, `github-event-action`, `github-event-base64`)
   - Any custom parameters defined in `event-to-circleci-parameters-mappings`

4. Your CircleCI workflow's conditional `when` clauses can filter which workflows to run based on the event type and action
5. Your jobs can access the parameters via the `<< pipeline.parameters.PARAMETER_NAME >>` syntax

### Step 4: Using the CircleCI Orb for Easier Parameter Handling

For Ethereum Optimism projects, you can use our custom CircleCI orb to simplify parameter handling:

```yaml
orbs:
  utils: ethereum-optimism/circleci-utils@1.0.19

jobs:
  process_github_event:
    docker:
      - image: cimg/base:current
    steps:
      - checkout
      - utils/github-event-handler-setup:
          github_event_base64: << pipeline.parameters.github-event-base64 >>
          env_prefix: "github_"
      - run:
          name: Use GitHub Event Data
          command: |
            # The orb has decoded the base64 data and set environment variables
            # with the specified prefix
            echo "Processing PR #$github_pull_request_number"
            echo "Label: $github_label_name"
            
            # Use any other extracted parameters as environment variables
            # All keys from the event-to-github-event-mappings will be 
            # available as environment variables prefixed with "github_"
```

The `utils/github-event-handler-setup` orb step:

1. Automatically decodes the `github-event-base64` parameter
2. Extracts all fields from the decoded JSON data
3. Sets each field as an environment variable with the specified prefix (e.g., `github_`)

For example, if your `event-to-github-event-mappings` includes:
```yaml
event-to-github-event-mappings:
  - pull_request_number: .pull_request.number
  - label_name: .label.name
  - repository_name: .repository.name
```

The orb will create these environment variables:
- `$github_pull_request_number`
- `$github_label_name`
- `$github_repository_name`

This makes it much easier to access the event data in your CircleCI jobs without having to manually decode and parse the base64 data.

### Example: React to PR Labels

This example shows how to trigger a specific workflow when a PR is labeled with "bug":

```yaml
# In github-event-handler.yml
version: 2
listen-for-events:
  pull_request:
    types: [labeled]
    filters:
      label.name: "bug"
    event-to-circleci-parameters-mappings:
      - pull_request_number: .pull_request.number
      - label_name: .label.name

# In config.yml workflow section
bug_triage:
  when:
    and:
      - equal: [<< pipeline.parameters.github-event-type >>, "pull_request"]
      - equal: [<< pipeline.parameters.github-event-action >>, "labeled"]
      - equal: [<< pipeline.parameters.label_name >>, "bug"] #only for version 2
  jobs:
    - run_bug_triage_job
```

With this setup, the workflow will only run when a PR is labeled specifically with "bug".
