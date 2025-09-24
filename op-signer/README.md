# @eth-optimism/signer

Signer service and client library

## Setup

Install go1.21

```shell
make build

# if not using direnv
source .envrc
./bin/signer
```

## Configuration

`op-signer` uses a `config.yaml` file to configure KMS providers and clients:

```yaml
# the KMS provider to use, must be one of: GCP (default), AWS, LOCAL
provider: GCP
# a list of client auth configurations
auth:
    # DNS name of the client connecting to op-signer
    # Must match the DNS name in the TLS certificate
  - name: localhost
    # key locator for the KMS provider
    key: projects/my-gcp-project/locations/my-region/keyRings/my-ring/cryptoKeys/my-key/cryptoKeyVersions/1
    # chain ID the signer can sign transactions for [optional]
    chainID: 00000000
    # sender address that is sending the RPC request [optional]
    fromAddress: 0x0000000000000000000000000000000000000000
    # addresses the sender is authorized to send transactions to [optional]
    toAddresses:
      - 0x0000000000000000000000000000000000000000
    # hex-encoded maximum transaction value [optional]
    maxValue: "0x0"
```

### KMS Providers

`op-signer` supports multiple KMS providers:

- `GCP`: Google Cloud Platform's Key Management Service
  > GCP credentials must be provided
- `AWS`: Amazon Web Service's Key Management Service
  > AWS credentials must be provided
- `LOCAL`: local private key files

#### Local Provider

> ⚠️ DO NOT USE THE LOCAL PROVIDER IN PRODUCTION ENVIRONMENTS ⚠️

When using `op-signer` with the `LOCAL` KMS provider you must specify a path to a private key file in each `auth[].key` field.

The provider expects:

- `PEM`-formatted private key files
- containing a key in `SEC1` format
- using the `secp256k1` elliptic curve
- with explicitly encoded curve parameters.

You can use the following command to generate such a key:

```shell
openssl ecparam -name secp256k1 -genkey -noout -param_enc explicit -out "ec_private.pem"
```

## Testing with local TLS

An mTLS connection is required between `op-signer` and the requesting service.

To test services in your local environment:

1. run `./gen-local-creds.sh [all|ca|client|client_tls|client_key] [clients]`
2. Check that `/tls` folder has been created with certificates and keys
2. Set the appropriate flags (`tls.cert`, `tls.key`, `tls.ca`) to the corresponding files under `/tls/<client>`
