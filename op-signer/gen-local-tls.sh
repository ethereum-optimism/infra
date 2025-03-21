#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
TLS_DIR=$SCRIPT_DIR/tls

OPENSSL_IMAGE="alpine/openssl:3.3.3"

USER_UID=$(id -u)
USER_GID=$(id -g)

echo "Generating mTLS credentials for local development..."

mkdir -p "$TLS_DIR"

# Helper function to run openssl commands in docker
docker_openssl() {
    docker run --rm \
        -v "$TLS_DIR:/export" \
        -u "$USER_UID:$USER_GID" \
        "$OPENSSL_IMAGE" "$@"
}

if [ ! -f "$TLS_DIR/ca.crt" ]; then
    echo 'Generating CA'
    docker_openssl req -newkey rsa:2048 \
        -new -nodes -x509 \
        -days 365 \
        -sha256 \
        -out /export/ca.crt \
        -keyout /export/ca.key \
        -subj "/O=OP Labs/CN=root"
fi

echo 'Generating TLS certificate request'
docker_openssl genrsa -out /export/tls.key 2048

# Create a config file for the CSR
cat > $TLS_DIR/openssl.cnf << EOF
[req]
distinguished_name=req
[san]
subjectAltName=DNS:localhost
EOF

docker_openssl req -new -key /export/tls.key \
    -out /export/tls.csr \
    -subj "/O=OP Labs/CN=localhost" \
    -extensions san \
    -config /export/openssl.cnf

# Create the certificate
docker_openssl x509 -req -in /export/tls.csr \
    -sha256 \
    -CA /export/ca.crt \
    -CAkey /export/ca.key \
    -CAcreateserial \
    -out /export/tls.crt \
    -days 3 \
    -extfile /export/openssl.cnf

echo "TLS certificates generated successfully in $TLS_DIR"
