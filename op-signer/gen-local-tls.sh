#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
TLS_DIR="$SCRIPT_DIR/tls"

OPENSSL_IMAGE="alpine/openssl:3.3.3"

USER_UID=$(id -u)
USER_GID=$(id -g)

CERT_ORG_NAME="OP-Signer Local Org"
MOD_LENGTH=2048

# Check if we should use Docker (default to true if not set)
USE_DOCKER=${OP_SIGNER_GEN_TLS_DOCKER:-true}

# Helper function to run openssl commands
run_openssl() {
    if [ "$USE_DOCKER" = "true" ]; then
        docker run --rm \
            -v "$TLS_DIR:$TLS_DIR" \
            -u "$USER_UID:$USER_GID" \
            "$OPENSSL_IMAGE" "$@"
    else
        # Check if openssl is available locally
        if ! command -v openssl &> /dev/null; then
            echo "Error: OpenSSL is not installed locally. Please install OpenSSL or use Docker by setting OP_SIGNER_GEN_TLS_DOCKER=true"
            exit 1
        fi
        openssl "$@"
    fi
}

# Function to generate credentials for a single hostname
generate_host_credentials() {
    local hostname="$1"
    echo -e "\nGenerating client credentials for $hostname..."
    
    # Create a directory for this hostname's credentials
    mkdir -p "$TLS_DIR/$hostname"
    
    # Generate client key
    echo "Generating client key..."
    run_openssl genrsa -out "$TLS_DIR/$hostname/tls.key" "$MOD_LENGTH"

    local confFile="$TLS_DIR/$hostname/openssl.cnf"
    
    # Create a config file for the CSR
    cat > "$confFile" << EOF
[req]
distinguished_name=req
[san]
subjectAltName=DNS:$hostname
EOF
    
    echo "Generating client certificate signing request..."
    run_openssl req -new -key "$TLS_DIR/$hostname/tls.key" \
        -sha256 \
        -out "$TLS_DIR/$hostname/tls.csr" \
        -subj "/O=$CERT_ORG_NAME/CN=$hostname" \
        -extensions san \
        -config "$confFile"
    
    echo "Generating client certificate..."
    run_openssl x509 -req -in "$TLS_DIR/$hostname/tls.csr" \
        -sha256 \
        -CA "$TLS_DIR/ca.crt" \
        -CAkey "$TLS_DIR/ca.key" \
        -CAcreateserial \
        -out "$TLS_DIR/$hostname/tls.crt" \
        -days 3 \
        -extensions san \
        -extfile "$confFile"
}

# Get hostnames from command line arguments, fallback to localhost if none provided
CLIENT_HOSTNAMES=("$@")
if [ ${#CLIENT_HOSTNAMES[@]} -eq 0 ]; then
    CLIENT_HOSTNAMES=("localhost")
fi

echo "Generating mTLS credentials for local development..."
echo "Hostnames: ${CLIENT_HOSTNAMES[*]}"
echo "Using Docker: $USE_DOCKER"

mkdir -p "$TLS_DIR"

# Avoid regenerating the CA so it doesn't need to be trusted again
if [ ! -f "$TLS_DIR/ca.crt" ]; then
    echo -e "\nGenerating CA..."
    run_openssl req -newkey "rsa:$MOD_LENGTH" \
        -new -nodes -x509 \
        -days 365 \
        -sha256 \
        -out "$TLS_DIR/ca.crt" \
        -keyout "$TLS_DIR/ca.key" \
        -subj "/O=$CERT_ORG_NAME/CN=root"
fi

for hostname in "${CLIENT_HOSTNAMES[@]}"; do
    generate_host_credentials "$hostname"
done

echo -e "\nGenerating private key for the local KMS provider..."
run_openssl ecparam -name secp256k1 -genkey -noout -param_enc explicit \
  -out "$TLS_DIR/ec_private.pem"
