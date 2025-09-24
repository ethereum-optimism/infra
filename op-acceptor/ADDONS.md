# Concept

Some test-oriented constructs are not necessarily suitable for prod-like
deployments, as they might purposefully expose the system to manipulations that
require some amount of trust.

`op-acceptor` addons allow the implementation of such constructs by the test
runner itself. Doing so allows the tests to be presented with those (privileged)
interfaces without making them available to a wider audience.

## op-faucet

This addon provides an unrestricted
[op-faucet](https://github.com/ethereum-optimism/optimism/tree/develop/op-faucet)
interface to a test, backed by pre-existing funded wallets.

The addon relies on specific information being present in the devnet descriptor
(https://github.com/ethereum-optimism/optimism/blob/develop/devnet-sdk/descriptors/deployment.go)
that it receives from kurtosis or netchef, namely the wallets to use (with
private keys of course).

- For L1:
  - either a wallet named `l1Faucet`
  - or a wallet named `user-key-20` (used by convention in local devnet deployments)
  - or a wallet named `l1Faucet` in any of the declared L2s (following `op-deployer`'s conventions for `wallets.json`)
- For each L2:
  - a wallet named `l2Faucet` (following `op-deployer`'s conventions for `wallets.json`)
