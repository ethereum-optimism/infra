# op-spamoor

op-spamoor is a wrapper around [spamoor-daemon](https://github.com/ethpandaops/spamoor), automatically selecting and funding the master account using the [op-faucet](https://github.com/ethereum-optimism/optimism/).

`op-spamoor`-specific environment variables:

- `ETH_RPC_URL` (default: `http://localhost:8545`): Ethereum EL endpoint.
- `FAUCET_URL` (default: `http://localhost:8546`): the faucet endpoint.
- `MIN_BALANCE` (default: 1 ETH): the minimum balance the master account can have. On startup, the account will be funded with this much ETH. Note that it is never funded after this point.
- `DAEMON_BIN_PATH` (default: `./spamoor-daemon`): the path to the `spamoor-daemon` binary.

All command line arguments are passed to the `spamoor-daemon`.

Example (assumes the EL endpoint and faucet are available on the stated endpoints):

```bash
docker build -t op-spamoor .
docker run \
  -e "ETH_RPC_URL=http://host.docker.internal:8545" \
  -e "FAUCET_URL=http://host.docker.internal:8546" \
  -p 8080:8080 \
  -p 8545:8545 \
  -p 8546:8546 \
  op-spamoor --rpchost http://host.docker.internal:8545
```
