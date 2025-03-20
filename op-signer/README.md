# @eth-optimism/signer

Signer service and client library

## Setup

Install go1.21

```bash
make build

source .env.example # (or copy to .envrc if using direnv)
./bin/signer
```

## Configuring KMS
Modify the `config.yaml` file to connect op-signer with your KMS.
- `name`: DNS name of the client connecting to op-signer. Must match the DNS name in the TLS certificate.
- `key`: key resource name of the KMS.

You can add a list of `name`/`key` to use different keys for each client connecting with op-signer.

## Testing with local tls
Running op-signer requires mTLS connection between the op-signer and the requesting server.

To test services in your local environment
1. run `./gen-local-tls.sh`
2. Check that `/tls` folder has been created with certificates and keys
2. Set the appropriate flags (`tls.cert`, `tls.key`, `tls.ca`) to the corresponding files under `/tls`
