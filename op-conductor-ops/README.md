# op-conductor-ops

op-conductor-ops is a CLI tool for managing op-conductor sequencer clusters.

**WARNING!!! This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using.**

## Install

### Via op-toolbox (recommended for operators)

```sh
op-toolbox install op-conductor-ops
op-toolbox op-conductor-ops status <network-name>
```

### Direct binary download

Releases attach a self-contained executable per platform; no Python is required.

```sh
version=0.2.0
platform=darwin-arm64  # or darwin-amd64, linux-amd64, linux-arm64
gh release download "op-conductor-ops/v${version}" \
  --repo ethereum-optimism/infra \
  --pattern "op-conductor-ops-${version}-${platform}"
chmod +x "op-conductor-ops-${version}-${platform}"
./op-conductor-ops-${version}-${platform} --help
```

Verify the download against `op-conductor-ops_${version}_checksums.txt` from the
same release.

### From source (development)

Requires [poetry](https://github.com/python-poetry/poetry).

```sh
poetry install
poetry run op-conductor-ops --help
```

Build the executable locally with `just build-binary 0.2.0 darwin-arm64`.

## Configuration

Recommended addition to your `.bashrc`/`.zshrc`:

```sh
export CONDUCTOR_CONFIG="<path-to-op-conductor-ops-config.toml>"
```

## Usage

```sh
# Implicit config lookup at ./config.toml or $CONDUCTOR_CONFIG
op-conductor-ops status <network-name>

# Explicit config and certificate paths
op-conductor-ops -c ./<path>/config.toml --cert ./<path>/cacert.pem <command> <network-name>
```

From a source checkout, prefix the above with `poetry run`.

## Example Configuration File: example.config.toml

This configuration file is used to set up the networks and sequencers for your application.

### Structure

The configuration file is divided into two main sections:

1. **Networks**: This section defines the networks that your application will use. There is an example network configuration (`op-network-1`) and a blank network configuration (`op-network-N`) for you to fill out.

2. **Sequencers**: This section defines the sequencers for each network. Again, there is an example sequencer configuration for `op-network-1` and a blank sequencer configuration for `op-network-N`.

Is is recommended to update the network name and sequencer names for your specifc configuration in the toml object declaration

### Config Usage

1. Copy this file to `config.toml` in your application's root directory.
2. Modify the example configurations or fill out the blank configurations as needed for your application.
3. Save the `config.toml` file and use it to configure your application's networks and sequencers.

Remember, the example configurations are provided for your convenience, but you should review and update them to match your specific requirements.
