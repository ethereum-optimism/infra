# Optimism Infrastructure

This repository is an extension of the [Optimism monorepo](https://github.com/ethereum-optimism/optimism) and contains the infrastructure that supports the Optimism ecosystem.

## Components
- op-conductor-mon: Monitors multiple op-conductor instances and provides a unified interface for reporting metrics.
- op-signer: Thin gateway that supports various RPC endpoints to sign payloads from op-stack components using private key stored in KMS.
- op-ufm: User facing monitoring creates transactions at regular intervals and observe transaction propagation across different RPC providers.

## Release Process

For the thoroughly defined process releasing services in this repository, please refer to [this document](./RELEASE.md).