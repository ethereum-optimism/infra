#!/usr/bin/env bash

# This script is based on the one at https://github.com/jklukas/git-push-fork-to-upstream-branch/blob/master/git-push-fork-to-upstream-branch.

set -euo pipefail

FORK_REPO="$0"
SOURCE_BRANCH="$1"
UPSTREAM_BRANCH="$2"
REQUESTED_SHA="$3"
PRIVATE_KEY_FILE="$4"

echo "Fork repo: $FORK_REPO"
echo "Source branch: $SOURCE_BRANCH"
echo "Upstream branch: $UPSTREAM_BRANCH"
echo "Requested sha: $REQUESTED_SHA"
echo "PK file: $PRIVATE_KEY_FILE"

export GIT_SSH_COMMAND="ssh -i '$PRIVATE_KEY_FILE' -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"

echo "Removing old remote..."
git remote remove fork-to-push || true
echo "Adding new remote..."
git remote add fork-to-push "git@github.com:$FORK_REPO"
echo "Fetching remote..."
git fetch fork-to-push

echo "Fetching remote head..."
head=$(git rev-parse "refs/remotes/fork-to-push/$SOURCE_BRANCH")
echo "Got remote head $head."

# bail if requested sha is not the same as the current sha
echo "Validating remote head..."
if [ "$REQUESTED_SHA" != "$head" ]; then
  echo "Requested sha $REQUESTED_SHA does not match current sha $head"
  exit 1
fi

echo "Pushing..."
git push --force origin "refs/remotes/fork-to-push/$SOURCE_BRANCH:refs/heads/$UPSTREAM_BRANCH"
echo "Removing remote..."
git remote remove fork-to-push