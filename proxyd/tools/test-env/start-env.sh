#!/bin/bash

# Source your environment variables
source .env

# Iterate through all arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        -build)
            echo "-build argument detected"
            pushd ../../
            make proxyd
            cd ..
            docker buildx build -f ./proxyd/Dockerfile -t ${IMAGE_NAME}:${IMAGE_TAG} . --platform linux/arm64
            popd
            ;;
        *)
            # Handle other arguments if needed
            ;;
    esac
    shift # Move to the next argument
done


# Prepare the config file
envsubst < ./proxyd/proxyd/proxyd.toml.template > ./proxyd/proxyd/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-1/proxyd.toml.template > ./proxyd/upstream-proxyd-1/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-2/proxyd.toml.template > ./proxyd/upstream-proxyd-2/proxyd.toml


# Start Docker Compose
docker-compose up -d --force-recreate
