#!/bin/bash

# Source your environment variables
source .env

# Retrieve the architecture of the host machine
HOST_ARCH=$(docker info --format '{{.Architecture}}')

# Map host architecture to Docker platform
case "$HOST_ARCH" in
  x86_64)
    PLATFORM="linux/amd64"
    ;;
  aarch64)
    PLATFORM="linux/arm64"
    ;;
  *)
    echo "Unsupported architecture: $HOST_ARCH"
    exit 1
    ;;
esac

# Iterate through all arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        build)
            echo "build argument detected"
            echo "building proxyd..."
            pushd ../../
            make proxyd
            cd ..
            docker buildx build -f ./proxyd/Dockerfile -t ${IMAGE_NAME}:${IMAGE_TAG} . --platform ${PLATFORM}
            popd
            echo "build complete"
            ;;
        *)
            ;;
    esac
    shift
done


# Prepare the config file
envsubst < ./proxyd/proxyd/template.proxyd.toml> ./proxyd/proxyd/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-1/template.proxyd.toml > ./proxyd/upstream-proxyd-1/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-2/template.proxyd.toml > ./proxyd/upstream-proxyd-2/proxyd.toml
envsubst < ./proxyd/upstream-proxyd-3/template.proxyd.toml > ./proxyd/upstream-proxyd-3/proxyd.toml


# Start Docker Compose
echo "deploying..."
docker-compose up -d --force-recreate
echo "deployment complete"
