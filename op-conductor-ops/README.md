# op-conductor-ops

op-conductor-ops is a CLI tool for managing op-conductor sequencer clusters.

**WARNING!!! This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using.**

## Setup

Requires [poetry](https://github.com/python-poetry/poetry).

Install the package and its dependencies with `poetry install`. This registers the
`op-conductor-ops` console script (see `[tool.poetry.scripts]` in `pyproject.toml`).

Recommended update to your .bashrc/zshrc:

1. `export CONDUCTOR_CONFIG="<path-to-op-conductor-ops-config.toml>"`

## Usage

After installing with `poetry install`, the tool is invoked with `poetry run op-conductor-ops`
(or just `op-conductor-ops` from within `poetry shell`), passing on any arguments.

### Example Usage

* Example usage with implicit config file with lookup at ./config.toml
```poetry run op-conductor-ops status <network-name>```

* Usage with explicit path to config and certificate
```poetry run op-conductor-ops -c ./<path>/config.toml --cert ./<path>/cacert.pem <command> <network-name>```

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
