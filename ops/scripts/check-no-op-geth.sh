#!/usr/bin/env bash
# Fails if any Go module can build against op-geth instead of upstream go-ethereum.
#
# Modules build against upstream go-ethereum and the current monorepo
# (ethereum-optimism/protocol-team#385). Go code imports go-ethereum the same
# way on either, so only the module files show the fork:
#   - a go.mod or go.sum that names ethereum-optimism/op-geth;
#   - any replace of github.com/ethereum/go-ethereum, which is how op-geth, a
#     local checkout of it, or any other fork is swapped in;
#   - a go.work, whose `use` or `replace` can swap in a local checkout. infra's
#     modules build on their own, so none is committed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

failed=0
fail() {
	echo "error: $1"
	failed=1
}

while IFS= read -r f; do
	if grep -q 'ethereum-optimism/op-geth' "$f"; then
		fail "$f depends on github.com/ethereum-optimism/op-geth."
	fi
done < <(git ls-files ':(glob)**/go.mod' ':(glob)**/go.sum')

# Matches `replace X => Y` and a `X => Y` line inside a replace block, with or
# without a version on X and with X quoted or not.
geth_replace='^[[:space:]]*(replace[[:space:]]+)?"?github\.com/ethereum/go-ethereum"?([[:space:]]+v[^[:space:]]+)?[[:space:]]*=>'
while IFS= read -r f; do
	if grep -Eq "$geth_replace" "$f"; then
		fail "$f replaces github.com/ethereum/go-ethereum."
	fi
done < <(git ls-files ':(glob)**/go.mod')

while IFS= read -r f; do
	fail "$f is committed: a workspace can swap in a local go-ethereum checkout."
done < <(git ls-files ':(glob)**/go.work')

if [ "$failed" -ne 0 ]; then
	echo "Build against upstream go-ethereum and the current monorepo instead; see ethereum-optimism/protocol-team#385."
fi
exit "$failed"
