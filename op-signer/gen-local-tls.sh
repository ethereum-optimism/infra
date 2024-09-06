#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
TLS_DIR=$SCRIPT_DIR/tls

version=$(openssl version)

if [[ "$version" != "LibreSSL"* ]] && [[ "$version" != "OpenSSL 1.1"* ]]; then
  echo "openssl version: $version"
  echo "script only works with LibreSSL (darwin) or OpenSSL 1.1*"
  exit 1
fi

echo "Generating mTLS credentials for local development..."
echo ""

mkdir -p "$TLS_DIR"

if [ ! -f "$TLS_DIR/ca.crt" ]; then
  echo 'Generating CA'
  openssl req -newkey rsa:2048 \
    -new -nodes -x509 \
    -days 365 \
    -sha256 \
    -out "$TLS_DIR/ca.crt" \
    -keyout "$TLS_DIR/ca.key" \
    -subj "/O=OP Labs/CN=root"
fi

echo 'Generating TLS certificate request'
openssl genrsa -out "$TLS_DIR/tls.key" 2048
openssl req -new -key "$TLS_DIR/tls.key" \
  -days 1 \
  -sha256 \
  -out "$TLS_DIR/tls.csr" \
  -keyout "$TLS_DIR/tls.key" \
  -subj "/O=OP Labs/CN=localhost" \
  -extensions san \
  -config <(echo '[req]'; echo 'distinguished_name=req'; \
            echo '[san]'; echo 'subjectAltName=DNS:localhost')

openssl x509 -req -in "$TLS_DIR/tls.csr" \
  -sha256 \
  -CA "$TLS_DIR/ca.crt" \
  -CAkey "$TLS_DIR/ca.key" \
  -CAcreateserial \
  -out "$TLS_DIR/tls.crt" \
  -days 3 \
  -extfile <(echo 'subjectAltName=DNS:localhost')
