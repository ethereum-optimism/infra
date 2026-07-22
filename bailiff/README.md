# bailiff

Bailiff is a PR bot that whitelists commits from external contributors. When triggered, it pushes specific commits
from a forked PR into the main repository. This ensures that all external PRs go through a round of manual review
before running in CI, which helps prevent security issues like secret exfiltration.

## How it works

1. Bailiff listens for PR comments on the main repository via a webhook.
2. A whitelisted user posts a trigger comment on the PR. The trigger comment must reference the exact commit to be
   whitelisted. This prevents a race condition where a malicious user could push a new commit to the PR after the
   comment is posted but before Bailiff clones the forked repository. Users are whitelisted based on their 
   membership in a GitHub team defined by the `admin_teams` directive in the config file. 
3. Bailiff clones the forked repository and pushes the commit referenced in the trigger comment to the main repository.

## Usage

Bailiff takes environment variables and a config file. See the [example config file](./config.example.yml) and
[example environment file](./.env.example) for how to use these.

To build bailiff, run `just build`. Then run `./dist/bailiff --config-file <config-file>` to run the daemon.

## Required Permissions

Bailiff needs the following permissions:

### Token Permissions

- Commit Status: Read/Write
- Pull Requests: Read/Write
- Repository: Read

### SSH Permissions

- Repository: Read/Write