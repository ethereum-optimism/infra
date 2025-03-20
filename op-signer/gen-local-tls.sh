#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
TLS_DIR=$SCRIPT_DIR/tls

version=$(openssl version)

if [[ "$version" == "LibreSSL"* ]]; then
  echo "openssl version: $version"
  echo "script only works with LibreSSL (darwin)"
  exit 1
fi

minimumVersion="1.1"
currentVersion="$(echo "$version" | awk '{print $2}')"

if [[ "$(printf '%s\n' "$minimumVersion" "$currentVersion" | sort -V | head -n1)" != "$minimumVersion" ]]; then
  echo "openssl version: $currentVersion"
  echo "minimum required version: $minimumVersion"
  echo "please upgrade OpenSSL to >= $minimumVersion"
  exit 1
fi

echo -e "Generating mTLS credentials for local development...\n"

mkdir -p "$TLS_DIR"

if [ ! -f "$TLS_DIR/ca.crt" ]; then
  echo "Generating CA"
  openssl req -newkey rsa:2048 \
    -new -nodes -x509 \
    -days 365 \
    -sha256 \
    -out "$TLS_DIR/ca.crt" \
    -keyout "$TLS_DIR/ca.key" \
    -subj "/O=OP Labs/CN=root"
fi

hostname="localhost"
altName="subjectAltName=DNS:$hostname"

echo "Generating TLS certificate request"
openssl genrsa -out "$TLS_DIR/tls.key" 2048
openssl req -new -key "$TLS_DIR/tls.key" \
  -sha256 \
  -out "$TLS_DIR/tls.csr" \
  -subj "/O=OP Labs/CN=$hostname" \
  -extensions san \
  -config <(echo "[req]"; echo "distinguished_name=req"; \
            echo "[san]"; echo "$altName")

openssl x509 -req -in "$TLS_DIR/tls.csr" \
  -sha256 \
  -CA "$TLS_DIR/ca.crt" \
  -CAkey "$TLS_DIR/ca.key" \
  -CAcreateserial \
  -out "$TLS_DIR/tls.crt" \
  -days 3 \
  -extfile <(echo "$altName")
