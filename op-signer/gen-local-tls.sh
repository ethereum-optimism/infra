#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
TLS_DIR="$SCRIPT_DIR/tls"
EXPORT_DIR="/export"

OPENSSL_IMAGE="alpine/openssl:3.3.3"

USER_UID=$(id -u)
USER_GID=$(id -g)

CERT_ORG_NAME="OP-Signer Local Org"
CLIENT_HOSTNAME="localhost"

echo "Generating mTLS credentials for local development......."

mkdir -p "$TLS_DIR"

# Helper function to run openssl commands in docker
docker_openssl() {
    docker run --rm \
        -v "$TLS_DIR:$EXPORT_DIR" \
        -u "$USER_UID:$USER_GID" \
        "$OPENSSL_IMAGE" "$@"
}

MOD_LENGTH=2048

# Avoid regenerating the CA so it doesn't need to be trusted again
if [ ! -f "$TLS_DIR/ca.crt" ]; then
    echo "Generating CA...."
    docker_openssl req -newkey "rsa:$MOD_LENGTH" \
        -new -nodes -x509 \
        -days 365 \
        -sha256 \
        -out "$EXPORT_DIR/ca.crt" \
        -keyout "$EXPORT_DIR/ca.key" \
        -subj "/O=$CERT_ORG_NAME/CN=root"
fi

echo "Generating client key...."
docker_openssl genrsa -out "$EXPORT_DIR/tls.key" "$MOD_LENGTH"

# Create a config file for the CSR
cat > "$TLS_DIR/openssl.cnf" << EOF
[req]
distinguished_name=req
[san]
subjectAltName=DNS:$CLIENT_HOSTNAME
EOF

echo "Generating client certificate signing request...."
docker_openssl req -new -key "$EXPORT_DIR/tls.key" \
    -sha256 \
    -out "$EXPORT_DIR/tls.csr" \
    -subj "/O=$CERT_ORG_NAME/CN=$CLIENT_HOSTNAME" \
    -extensions san \
    -config "$EXPORT_DIR/openssl.cnf"

echo "Generating client certificate...."
docker_openssl x509 -req -in "$EXPORT_DIR/tls.csr" \
    -sha256 \
    -CA "$EXPORT_DIR/ca.crt" \
    -CAkey "$EXPORT_DIR/ca.key" \
    -CAcreateserial \
    -out "$EXPORT_DIR/tls.crt" \
    -days 3 \
    -extensions san \
    -extfile "$EXPORT_DIR/openssl.cnf"

echo "Generating EC private key for the local KMS provider...."
docker_openssl ecparam -name secp256k1 -genkey -noout -param_enc explicit \
  -out "$EXPORT_DIR/ec_private.pem"
