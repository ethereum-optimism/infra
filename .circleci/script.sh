#bin/sh
GIT_COMMIT=$(git rev-parse HEAD)
echo "Checking git tags pointing at $GIT_COMMIT:"
tags_at_commit=$(git tag --points-at $GIT_COMMIT)
echo "Tags at commit:\n$tags_at_commit"

filtered_tags=$(echo "$tags_at_commit" | grep "^proxyd/" || true)
echo "Filtered tags: $filtered_tags"

if [ -z "$filtered_tags" ]; then
  export GIT_VERSION="untagged"
else
  sorted_tags=$(echo "$filtered_tags" | sed "s/proxyd\///" | sort -V)
  echo "Sorted tags: $sorted_tags"

  full_release_tag=$(echo "$sorted_tags" | grep -v -- "-rc" || true)
  if [ -z "$full_release_tag" ]; then
    export GIT_VERSION=$(echo "$sorted_tags" | tail -n 1)
  else
    export GIT_VERSION=$(echo "$full_release_tag" | tail -n 1)
  fi
fi

echo "Setting GIT_VERSION=$GIT_VERSION"
