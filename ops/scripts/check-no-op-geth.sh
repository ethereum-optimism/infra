#!/usr/bin/env bash
# Fails if any Go module depends on github.com/ethereum-optimism/op-geth.
#
# Modules build against upstream go-ethereum and the current monorepo
# (ethereum-optimism/protocol-team#385). Go code imports go-ethereum the same
# way on either, so only go.mod shows the fork: a `replace` or `require` of it.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

failed=0
while IFS= read -r mod; do
	if grep -q 'ethereum-optimism/op-geth' "$mod"; then
		echo "error: $mod depends on github.com/ethereum-optimism/op-geth."
		echo "  Build against upstream go-ethereum and the current monorepo instead; see ethereum-optimism/protocol-team#385."
		failed=1
	fi
done < <(git ls-files ':(glob)**/go.mod')

exit "$failed"
