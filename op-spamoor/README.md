# op-spamoor

op-spamoor is a wrapper around [spamoor-daemon](https://github.com/ethpandaops/spamoor), automatically selecting and funding the master account using the [op-faucet](https://github.com/ethereum-optimism/optimism/).

`op-spamoor`-specific environment variables:

- `ETH_RPC_URL` (default: `http://localhost:8545`): Ethereum EL endpoint.
- `FAUCET_URL` (default: `http://localhost:8546`): the faucet endpoint.
- `MIN_BALANCE` (default: 1 ETH): the minimum balance the master account can have. On startup, the account will be funded with this much ETH. Note that it is never funded after this point.
- `DAEMON_BIN_PATH` (default: `./spamoor-daemon`): the path to the `spamoor-daemon` binary.

All command line arguments are passed to the `spamoor-daemon`.

To try it out, run `docker compose up` and point your browser at [http://localhost:8080](http://localhost:8080).
