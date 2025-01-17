# Optimism Infrastructure

This repository is an extension of the [Optimism monorepo](https://github.com/ethereum-optimism/optimism) and contains the infrastructure that supports the Optimism ecosystem.

## Components
- op-conductor-mon: Monitors multiple op-conductor instances and provides a unified interface for reporting metrics.
- op-signer: Thin gateway that supports various RPC endpoints to sign payloads from op-stack components using private key stored in KMS.
- op-ufm: User facing monitoring creates transactions at regular intervals and observe transaction propagation across different RPC providers.

## Release Process

For the thoroughly defined process releasing services in this repository, please refer to [this document](./RELEASE.md).

## Development

### Dependencies

#### Using `mise`

We use [`mise`](https://mise.jdx.dev/) as a dependency manager for these tools.
Once properly installed, `mise` will provide the correct versions for each tool. `mise` does not
replace any other installations of these binaries and will only serve these binaries when you are
working inside of the `optimism` directory.

##### Install `mise`

Install `mise` by following the instructions provided on the
[Getting Started page](https://mise.jdx.dev/getting-started.html#_1-install-mise-cli).

##### Install dependencies

```sh
mise install
```