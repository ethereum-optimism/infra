#!/usr/bin/env python
import concurrent.futures

import requests
from rich.console import Console
from rich.table import Table
import typer

from config import NETWORKS, CERT_PATH
from utils import make_rpc_payload, print_boolean

app = typer.Typer(
    help="CLI for managing OP Conductor sequencers. WARNING: This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using."
)
console = Console()


def get_network(network: str):
    if network not in NETWORKS:
        typer.echo(f"Network must be one of {', '.join(NETWORKS.keys())}")
        raise typer.Exit(code=1)
    network_obj = NETWORKS[network]
    network_obj.update()
    return network_obj


@app.command()
def status(network: str):
    """Print the status of all sequencers in a network."""
    network_obj = get_network(network)
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

    leader = network_obj.find_conductor_leader()
    if leader is None:
        typer.echo(f"Could not find current leader in network {network}")
    else:
        display_correction = False
        membership = {x["id"]: x for x in leader.cluster_membership()}
        for sequencer in sequencers:
            if sequencer.sequencer_id in membership:
                if (
                    int(not sequencer.voting)
                    != membership[sequencer.sequencer_id]["suffrage"]
                ):
                    typer.echo(f"WARNING: {sequencer.sequencer_id} does not have the correct voting status.")
                    display_correction = True
            else:
                typer.echo(f"WARNING: {sequencer.sequencer_id} is not in the cluster")
                display_correction = True
        if display_correction:
              typer.echo("Run 'update-cluster-membership' to correct membership issues")


@app.command()
def transfer_leader(network: str, sequencer_id: str):
    """Transfer leadership to a specific sequencer."""
    network_obj = get_network(network)

    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)
    if sequencer.voting is False:
        typer.echo(f"Sequencer {sequencer_id} is not a voter")
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
        verify=CERT_PATH,
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
    network_obj = get_network(network)
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
            verify=CERT_PATH,
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
    network_obj = get_network(network)
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
            verify=CERT_PATH,
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
    """Override the conductor_leader response for a sequencer to True.
    Note that this does not affect consensus and it should only be used for disaster recovery purposes.
    """
    network_obj = get_network(network)
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    resp = requests.post(
        sequencer.rpc_url,
        json=make_rpc_payload("conductor_overrideLeader"),
        verify=CERT_PATH,
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(
            f"Failed to override conductor leader status for {sequencer_id}: {resp.json()['error']}"
        )
        raise typer.Exit(code=1)

    resp = requests.post(
        sequencer.node_rpc_url,
        json=make_rpc_payload("admin_overrideLeader"),
        verify=CERT_PATH,
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(
            f"Failed to override sequencer leader status for {sequencer_id}: {resp.json()['error']}"
        )
        raise typer.Exit(code=1)

    typer.echo(f"Successfully overrode leader for {sequencer_id}")


@app.command()
def remove_server(network: str, sequencer_id: str):
    """Remove a sequencer from the cluster."""
    network_obj = get_network(network)
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    leader = network_obj.find_conductor_leader()

    resp = requests.post(
        leader.rpc_url,
        json=make_rpc_payload("conductor_removeServer", params=[sequencer_id, 0]),
        verify=CERT_PATH,
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(f"Failed to remove {sequencer_id}: {resp.json()['error']}")
        raise typer.Exit(code=1)

    typer.echo(f"Successfully removed {sequencer_id}")


@app.command()
def update_cluster_membership(network: str):
    """Update the cluster membership to match the sequencer configuration."""
    network_obj = get_network(network)

    sequencers = network_obj.sequencers

    leader = network_obj.find_conductor_leader()
    if leader is None:
        typer.echo(f"Could not find current leader in network {network}")
        raise typer.Exit(code=1)

    membership = {x["id"]: x for x in leader.cluster_membership()}

    error = False
    for sequencer in sequencers:
        if sequencer.sequencer_id in membership:
            if (
                int(not sequencer.voting)
                != membership[sequencer.sequencer_id]["suffrage"]
            ):
                typer.echo(
                    f"Removing {sequencer.sequencer_id} from cluster to update voting status"
                )
                remove_server(network, sequencer.sequencer_id)
        method = (
            "conductor_addServerAsVoter"
            if sequencer.voting
            else "conductor_addServerAsNonvoter"
        )
        resp = requests.post(
            leader.rpc_url,
            json=make_rpc_payload(
                method,
                params=[sequencer.sequencer_id, sequencer.raft_addr, 0],
            ),
            verify=CERT_PATH,
        )
        try:
            resp.raise_for_status()
            if "error" in resp.json():
                raise Exception(resp.json()["error"])
            typer.echo(
                f"Successfully added {sequencer.sequencer_id} as {'voter' if sequencer.voting else 'non-voter'}"
            )
        except Exception as e:
            typer.echo(f"Failed to add {sequencer.sequencer_id} as voter: {e}")
    if error:
        raise typer.Exit(code=1)


if __name__ == "__main__":
    app()
