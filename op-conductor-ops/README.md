# op-conductor-ops

op-conductor-ops is a CLI tool for managing op-conductor sequencer clusters.

**WARNING!!! This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using.**

## Setup

Requires [poetry](https://github.com/python-poetry/poetry).

Install python dependencies with `poetry install`.

## Usage

After installing dependencies with `poetry`, the tool can be invoked with `./op-conductor-ops`,
which just calls `poetry run python main.py` and passes on any arguments.
