#!/usr/bin/env python
import concurrent.futures
import requests
from rich.console import Console
from rich.table import Table
import typer

cert_path = "./combined-cacert.pem"
app = typer.Typer(help="CLI for managing OP Conductor sequencers. WARNING: This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using.")
console = Console()


def print_boolean(value):
    if value is None:
        return "❓"
    return "✅" if value else "❌"


def make_rpc_payload(method: str, params: list = None):
    if params is None:
        params = []
    return {
        "id": 1,
        "jsonrpc": "2.0",
        "method": method,
        "params": params,
    }


class Sequencer:
    def __init__(self, sequencer_id, raft_addr, rpc_url, node_rpc_url):
        self.sequencer_id = sequencer_id
        self.raft_addr = raft_addr
        self.rpc_url = rpc_url
        self.node_rpc_url = node_rpc_url
        self.conductor_active = None
        self.conductor_leader = None
        self.sequencer_healthy = None
        self.sequencer_active = None
        self.unsafe_l2_hash = None
        self.unsafe_l2_number = None

    def _get_sequencer_active(self):
        resp = requests.post(
            self.node_rpc_url,
            json=make_rpc_payload("admin_sequencerActive"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.sequencer_active = resp.json()["result"]

    def _get_sequencer_healthy(self):
        resp = requests.post(
            self.rpc_url,
            json=make_rpc_payload("conductor_sequencerHealthy"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.sequencer_healthy = resp.json()["result"]

    def _get_conductor_active(self):
        resp = requests.post(
            self.rpc_url,
            json=make_rpc_payload("conductor_active"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.conductor_active = resp.json()["result"]

    def _get_conductor_leader(self):
        resp = requests.post(
            self.rpc_url,
            json=make_rpc_payload("conductor_leader"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.conductor_leader = resp.json()["result"]

    def _get_unsafe_l2(self):
        resp = requests.post(
            self.node_rpc_url,
            json=make_rpc_payload("optimism_syncStatus"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        result = resp.json()["result"]
        self.unsafe_l2_number = result["unsafe_l2"]["number"]
        self.unsafe_l2_hash = result["unsafe_l2"]["hash"]

    def update(self):
        functions = [
            self._get_conductor_active,
            self._get_conductor_leader,
            self._get_sequencer_healthy,
            self._get_sequencer_active,
            self._get_unsafe_l2,
        ]
        with concurrent.futures.ThreadPoolExecutor() as executor:
            futures = {executor.submit(func): func for func in functions}
            for future in concurrent.futures.as_completed(futures):
                func = futures[future]
                try:
                    result = future.result()
                except Exception as e:
                    typer.echo(f"{func.__name__} raised an exception: {e}")


class Network:
    def __init__(self, name, sequencers):
        self.name = name

        def update(sequencer):
            sequencer.update()

        with concurrent.futures.ThreadPoolExecutor() as executor:
            list(executor.map(update, sequencers))

        self.sequencers = sequencers

    def get_sequencer_by_id(self, sequencer_id: str):
        return next(
            (
                sequencer
                for sequencer in self.sequencers
                if sequencer.sequencer_id == sequencer_id
            ),
            None,
        )

    def find_conductor_leader(self):
        return next(
            (sequencer for sequencer in self.sequencers if sequencer.conductor_leader),
            None,
        )


networks = {
    "op-mainnet": Network(
        "op-mainnet",
        [
            Sequencer(
                "prod-mainnet-sequencer-0",
                "sequencer-0-op-conductor-raft.primary.mainnet.prod.oplabs.cloud:50050",
                "https://sequencer-0-op-conductor.primary.mainnet.prod.oplabs.cloud",
                "https://sequencer-0-op-node.primary.mainnet.prod.oplabs.cloud",
            ),
            Sequencer(
                "prod-mainnet-sequencer-1",
                "sequencer-1-op-conductor-raft.secondary.mainnet.prod.oplabs.cloud:50050",
                "https://sequencer-1-op-conductor.secondary.mainnet.prod.oplabs.cloud",
                "https://sequencer-1-op-node.secondary.mainnet.prod.oplabs.cloud",
            ),
            Sequencer(
                "prod-mainnet-sequencer-2",
                "sequencer-2-op-conductor-raft.tertiary.mainnet.prod.oplabs.cloud:50050",
                "https://sequencer-2-op-conductor.tertiary.mainnet.prod.oplabs.cloud",
                "https://sequencer-2-op-node.tertiary.mainnet.prod.oplabs.cloud",
            ),
        ],
    ),
    "op-sepolia": Network(
        "op-sepolia",
        [
            Sequencer(
                "prod-sepolia-sequencer-0",
                "sequencer-0-op-conductor-raft.primary.sepolia.prod.oplabs.cloud:50050",
                "https://sequencer-0-op-conductor.primary.sepolia.prod.oplabs.cloud",
                "https://sequencer-0-op-node.primary.sepolia.prod.oplabs.cloud",
            ),
            Sequencer(
                "prod-sepolia-sequencer-1",
                "sequencer-1-op-conductor-raft.primary.sepolia.prod.oplabs.cloud:50050",
                "https://sequencer-1-op-conductor.primary.sepolia.prod.oplabs.cloud",
                "https://sequencer-1-op-node.primary.sepolia.prod.oplabs.cloud",
            ),
            Sequencer(
                "prod-sepolia-sequencer-2",
                "sequencer-2-op-conductor-raft.primary.sepolia.prod.oplabs.cloud:50050",
                "https://sequencer-2-op-conductor.primary.sepolia.prod.oplabs.cloud",
                "https://sequencer-2-op-node.primary.sepolia.prod.oplabs.cloud",
            ),
        ],
    ),
    "conductor-dev": Network(
        "conductor-dev",
        [
            Sequencer(
                "dev-client-conductor-dev-sequencer-0",
                "conductor-dev-sequencer-0-op-conductor-raft.primary.client.dev.oplabs.cloud:50050",
                "https://conductor-dev-sequencer-0-op-conductor.primary.client.dev.oplabs.cloud",
                "https://conductor-dev-sequencer-0-op-node.primary.client.dev.oplabs.cloud",
            ),
            Sequencer(
                "dev-client-conductor-dev-sequencer-1",
                "conductor-dev-sequencer-1-op-conductor-raft.primary.client.dev.oplabs.cloud:50050",
                "https://conductor-dev-sequencer-1-op-conductor.primary.client.dev.oplabs.cloud",
                "https://conductor-dev-sequencer-1-op-node.primary.client.dev.oplabs.cloud",
            ),
            Sequencer(
                "dev-client-conductor-dev-sequencer-2",
                "conductor-dev-sequencer-2-op-conductor-raft.primary.client.dev.oplabs.cloud:50050",
                "https://conductor-dev-sequencer-2-op-conductor.primary.client.dev.oplabs.cloud",
                "https://conductor-dev-sequencer-2-op-node.primary.client.dev.oplabs.cloud",
            ),
        ],
    ),
}


@app.command()
def status(network: str):
    """Print the status of all sequencers in a network."""
    if network not in networks:
        typer.echo(f"Network must be one of {', '.join(networks.keys())}")
        raise typer.Exit(code=1)
    network_obj = networks[network]
    sequencers = network_obj.sequencers
    table = Table(
        "Sequencer ID",
        "Active",
        "Healthy",
        "Leader",
        "Sequencing",
        "Unsafe Number",
        "Unsafe Hash",
    )
    for sequencer in sequencers:
        table.add_row(
            sequencer.sequencer_id,
            print_boolean(sequencer.conductor_active),
            print_boolean(sequencer.sequencer_healthy),
            print_boolean(sequencer.conductor_leader),
            print_boolean(sequencer.sequencer_active),
            str(sequencer.unsafe_l2_number),
            str(sequencer.unsafe_l2_hash),
        )
    console.print(table)


@app.command()
def transfer_leader(network: str, sequencer_id: str):
    """Transfer leadership to a specific sequencer."""
    if network not in networks:
        typer.echo(f"Network must be one of {', '.join(networks.keys())}")
        raise typer.Exit(code=1)
    network_obj = networks[network]

    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    healthy = sequencer.sequencer_healthy
    if not healthy:
        typer.echo(f"Target sequencer {sequencer_id} is not healthy")
        raise typer.Exit(code=1)

    leader = network_obj.find_conductor_leader()
    if leader is None:
        typer.echo(f"Could not find current leader in network {network}")
        raise typer.Exit(code=1)

    resp = requests.post(
        leader.rpc_url,
        json=make_rpc_payload(
            "conductor_transferLeaderToServer",
            params=[sequencer.sequencer_id, sequencer.raft_addr],
        ),
        verify=cert_path,
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(
            f"Failed to transfer leader to {sequencer_id}: {resp.json()['error']}"
        )
        raise typer.Exit(code=1)

    typer.echo(f"Successfully transferred leader to {sequencer_id}")


@app.command()
def pause(network: str, sequencer_id: str = None):
    """Pause all conductors.
    If --sequencer-id is provided, only pause conductor for that sequencer.
    """
    if network not in networks:
        typer.echo(f"Network must be one of {', '.join(networks.keys())}")
        raise typer.Exit(code=1)
    network_obj = networks[network]
    sequencers = network_obj.sequencers

    if sequencer_id is not None:
        sequencer = network_obj.get_sequencer_by_id(sequencer_id)
        if sequencer is None:
            typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
            raise typer.Exit(code=1)
        sequencers = [sequencer]

    error = False
    for sequencer in sequencers:
        resp = requests.post(
            sequencer.rpc_url,
            json=make_rpc_payload("conductor_pause"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
            if "error" in resp.json():
                raise Exception(resp.json()["error"])
            typer.echo(f"Successfully paused {sequencer.sequencer_id}")
        except Exception as e:
            typer.echo(f"Failed to pause {sequencer.sequencer_id}: {e}")
    if error:
        raise typer.Exit(code=1)


@app.command()
def resume(network: str, sequencer_id: str = None):
    """Resume all conductors.
    If --sequencer-id is provided, only resume conductor for that sequencer.
    """
    if network not in networks:
        typer.echo(f"Network must be one of {', '.join(networks.keys())}")
        raise typer.Exit(code=1)
    network_obj = networks[network]
    sequencers = network_obj.sequencers

    if sequencer_id is not None:
        sequencer = network_obj.get_sequencer_by_id(sequencer_id)
        if sequencer is None:
            typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
            raise typer.Exit(code=1)
        sequencers = [sequencer]

    error = False
    for sequencer in sequencers:
        resp = requests.post(
            sequencer.rpc_url,
            json=make_rpc_payload("conductor_resume"),
            verify=cert_path,
        )
        try:
            resp.raise_for_status()
            if "error" in resp.json():
                raise Exception(resp.json()["error"])
            typer.echo(f"Successfully resumed {sequencer.sequencer_id}")
        except Exception as e:
            typer.echo(f"Failed to resume {sequencer.sequencer_id}: {e}")
    if error:
        raise typer.Exit(code=1)


@app.command()
def override_leader(network: str, sequencer_id: str):
    """
    Override the conductor_leader response for a sequencer to True.
    Note that this does not affect consensus and it should only be used for disaster recovery purposes.
    """
    if network not in networks:
        typer.echo(f"Network must be one of {', '.join(networks.keys())}")
        raise typer.Exit(code=1)
    network_obj = networks[network]
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    resp = requests.post(
        sequencer.rpc_url,
        json=make_rpc_payload("conductor_overrideLeader"),
        verify=cert_path,
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(
            f"Failed to override leader for {sequencer_id}: {resp.json()['error']}"
        )
        raise typer.Exit(code=1)

    typer.echo(f"Successfully overrode leader for {sequencer_id}")


if __name__ == "__main__":
    app()
