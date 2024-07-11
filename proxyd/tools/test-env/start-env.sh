#!/bin/bash

# Source your environment variables
source .env

# Prepare the config file
envsubst < ./proxyd/proxyd/proxyd.toml.template > ./proxyd/proxyd/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-1/proxyd.toml.template > ./proxyd/upstream-proxyd-1/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-2/proxyd.toml.template > ./proxyd/upstream-proxyd-2/proxyd.toml


# Start Docker Compose
docker-compose up -d
