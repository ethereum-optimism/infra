from .network import Network
from .sequencer import Sequencer
import os
import toml


def read_config(config_path: str) -> tuple[dict[str, Sequencer], str]:
    config = toml.load(config_path)

    cert_path = config.get("cert_path", "")
    # Resolve relative cert paths against the config file's directory
    # (not the config filename itself — bare names like "toss.toml" have no '/').
    if cert_path and not os.path.isabs(cert_path):
        config_dir = os.path.dirname(os.path.abspath(config_path))
        cert_path = os.path.normpath(os.path.join(config_dir, cert_path))

    # load sequencers into a map
    sequencers = {}
    for name, seq_config in config["sequencers"].items():

        sequencers[name] = Sequencer(
            sequencer_id=name,
            raft_addr=seq_config["raft_addr"],
            conductor_rpc_url=seq_config["conductor_rpc_url"],
            node_rpc_url=seq_config["node_rpc_url"],
            voting=seq_config["voting"],
            builder_rpc_url=seq_config.get("builder_rpc_url"),
            rollup_boost_rpc_url=seq_config.get("rollup_boost_debug_rpc_url"),
        )

    # Initialize network, with list of sequencers
    networks = {}
    for network_name, network_config in config["networks"].items():
        network_sequencers = [
            sequencers[seq_name] for seq_name in network_config["sequencers"]
        ]
        networks[network_name] = Network(network_name, network_sequencers)

    return networks, cert_path
