# Network Acceptance Tester (NAT)
NAT is a tool to run checks against a network to determine if it is ready for production.

## Building and Running

1. `just op-nat`
1. `./bin/op-nat --kurtosis.devnet.manifest=../kurtosis-devnet/tests/interop-devnet.json`

## Running locally with a monitoring stack

This will start a local monitoring stack with Prometheus and Grafana as well as NAT.

1. `just start-monitoring`

## Network targets

Target networks are defined in the `devnets` directory. They can be local or remote.
1. Local networks can be run from [kurtosis-devnet](https://github.com/ethereum-optimism/optimism/tree/develop/kurtosis-devnet) in the optimism monorepo.
1. Remote test networks can be found on the [devnets](https://devnets.optimism.io/) page.

## TODOs
See [here](https://www.notion.so/oplabs/17cf153ee1628001a06ce7231b775cf2?v=253881d1c6034d49b263baa4325b65f8).
