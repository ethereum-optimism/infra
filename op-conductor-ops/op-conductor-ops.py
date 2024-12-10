#!/usr/bin/env python

import os
import time
import requests
from rich.console import Console
from rich.table import Table
import typer
from typing_extensions import Annotated

from config import read_config
from utils import make_rpc_payload, print_boolean, print_warn, print_error


app = typer.Typer(
    help="CLI for managing OP Conductor sequencers. WARNING: This tool can cause a network outage if used improperly. Please consult #pod-devinfra before using."
)

console = Console()


@app.callback()
def load_config(
    cert: Annotated[str, typer.Option(
        "--cert",
        help="[Optional] Certificate file path for https. Takes precedece over cert_path config",
        envvar="CONDUCTOR_CERT",
    )] = "",
    config_path: Annotated[str, typer.Option(
        "--config", "-c",
        help="Path to config file.",
        envvar="CONDUCTOR_CONFIG",
    )] = "./config.toml",
):
    networks, config_cert_path = read_config(config_path)
    global NETWORKS
    NETWORKS = networks

    # Use the cert path from the command line if provided,
    # otherwise use the one from the config
    # Export the certificate for https connections
    cert_path = cert or config_cert_path
    if cert_path:
        os.environ["REQUESTS_CA_BUNDLE"] = cert_path
        os.environ["SSL_CERT_FILE"] = cert_path


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
        "Conductor Active",
        "Sequencer Healthy",
        "Conductor Leader",
        "Active Sequencer",
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
        print_warn(f"Could not find current leader in network {network}")
    else:
        display_correction = False
        membership = {x["id"]: x for x in leader.cluster_membership()}
        for sequencer in sequencers:
            if sequencer.sequencer_id in membership:
                if (
                    int(not sequencer.voting)
                    != membership[sequencer.sequencer_id]["suffrage"]
                ):
                    print_error(
                        f": {sequencer.sequencer_id} does not have the correct voting status.")
                    display_correction = True
            else:
                print_warn(
                    f": {sequencer.sequencer_id} is not in the cluster")
                display_correction = True
        if display_correction:
            print_warn(
                "Run 'update-cluster-membership' to correct membership issues")


@app.command()
def transfer_leader(network: str, sequencer_id: str, force: bool = False):
    """Transfer leadership to a specific sequencer."""
    network_obj = get_network(network)

    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        print_error(
            f"Sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)
    if sequencer.voting is False:
        print_error(f"Sequencer {sequencer_id} is not a voter")
        raise typer.Exit(code=1)

    healthy = sequencer.sequencer_healthy
    if not healthy and not force:
        print_error(f"Target sequencer {sequencer_id} is not healthy. To still perform the leadership transfer, please use --force.")
        raise typer.Exit(code=1)

    leader = network_obj.find_conductor_leader()
    if leader is None:
        print_error(f"Could not find current leader in network {network}")
        raise typer.Exit(code=1)

    resp = requests.post(
        leader.conductor_rpc_url,
        json=make_rpc_payload(
            "conductor_transferLeaderToServer",
            params=[sequencer.sequencer_id, sequencer.raft_addr],
        ),
    )
    resp.raise_for_status()
    if "error" in resp.json():
        print_error(
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
            print_error(
                f"Sequencer ID {sequencer_id} not found in network {network}")
            raise typer.Exit(code=1)
        sequencers = [sequencer]

    error = False
    for sequencer in sequencers:
        resp = requests.post(
            sequencer.conductor_rpc_url,
            json=make_rpc_payload("conductor_pause"),
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
            print_error(
                f"sequencer ID {sequencer_id} not found in network {network}")
            raise typer.Exit(code=1)
        sequencers = [sequencer]

    error = False
    for sequencer in sequencers:
        resp = requests.post(
            sequencer.conductor_rpc_url,
            json=make_rpc_payload("conductor_resume"),
        )
        try:
            resp.raise_for_status()
            if "error" in resp.json():
                raise Exception(resp.json()["error"])
            typer.echo(f"Successfully resumed {sequencer.sequencer_id}")
        except Exception as e:
            print_error(f"Failed to resume {sequencer.sequencer_id}: {e}")
    if error:
        raise typer.Exit(code=1)


@app.command()
def override_leader(network: str, sequencer_id: str, remove: bool = False, y: bool = False):
    """Override the conductor_leader response for a sequencer to True. To remove the override, please use --remove.
    If you want this command to be un-interactive, please use it with --y.
    Note that this does not affect consensus and it should only be used for disaster recovery purposes.
    """
    network_obj = get_network(network)
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        print_error(
            f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)
    
    if remove:
        if y:
            print_warn("You are trying to remove the override. This would require you to explicitly restart op-node.")
        else:
            typer.echo("Note: you are trying to remove the override. This would require you to explicitly restart op-node.")
            typer.echo("Please be carefully sure of that and proceed by entering 'y' or exit by entering 'n':")
            user_input = input().lower()
            if user_input not in ['y', 'n']:
                print_error("Wrong input provided")
                raise typer.Exit(code=1)
            if user_input == 'n':
                raise typer.Exit(code=0)

    resp = requests.post(
        sequencer.conductor_rpc_url,
        json=make_rpc_payload("conductor_overrideLeader", [not remove]),
    )
    resp.raise_for_status()
    if "error" in resp.json():
        print_error(
            f"Failed to override conductor leader status for {sequencer_id}: {resp.json()['error']}"
        )
        raise typer.Exit(code=1)

    if not remove:
        resp = requests.post(
            sequencer.node_rpc_url,
            json=make_rpc_payload("admin_overrideLeader"),
        )
        resp.raise_for_status()
        if "error" in resp.json():
            print_error(
                f"Failed to override sequencer leader status for {sequencer_id}: {resp.json()['error']}"
            )
            raise typer.Exit(code=1)

    typer.echo(f"Successfully overrode leader for {sequencer_id} to {not remove}")
    if remove:
        typer.echo("As you provided --remove, do remember to restart the op-node pod to remove the leadership-override from it.")


@app.command()
def remove_server(network: str, sequencer_id: str):
    """Remove a sequencer from the cluster."""
    network_obj = get_network(network)
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        print_error(
            f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    leader = network_obj.find_conductor_leader()

    resp = requests.post(
        leader.conductor_rpc_url,
        json=make_rpc_payload("conductor_removeServer",
                              params=[sequencer_id, 0]),
    )
    resp.raise_for_status()
    if "error" in resp.json():
        print_error(f"Failed to remove {sequencer_id}: {resp.json()['error']}")
        raise typer.Exit(code=1)

    typer.echo(f"Successfully removed {sequencer_id}")


@app.command()
def update_cluster_membership(network: str):
    """Update the cluster membership to match the sequencer configuration."""
    network_obj = get_network(network)

    sequencers = network_obj.sequencers

    leader = network_obj.find_conductor_leader()
    if leader is None:
        print_error(f"Could not find current leader in network {network}")
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
            leader.conductor_rpc_url,
            json=make_rpc_payload(
                method,
                params=[sequencer.sequencer_id, sequencer.raft_addr, 0],
            ),
        )
        try:
            resp.raise_for_status()
            if "error" in resp.json():
                raise Exception(resp.json()["error"])
            typer.echo(
                f"Successfully added {sequencer.sequencer_id} as {'voter' if sequencer.voting else 'non-voter'}"
            )
        except Exception as e:
            print_warn(f"Failed to add {sequencer.sequencer_id} as voter: {e}")
    if error:
        raise typer.Exit(code=1)

@app.command()
def halt_sequencer(network: str, force: bool = False):
    """Halts the currently active sequencer."""
    network_obj = get_network(network)
    sequencers = network_obj.sequencers

    all_conductors_paused = all(
        [not sequencer.conductor_active for sequencer in sequencers]
    )
    if not all_conductors_paused and not force:
        print_error(
            f"Not all conductors were found to be paused. Please run the `pause` command or use --force to override this behaviour"
        )
        raise typer.Exit(code=1)

    active_sequencer = network_obj.find_active_sequencer()
    if active_sequencer is None:
        print_error(f"Could not find an active sequencer in the network: {network}")
        raise typer.Exit(code=1)

    try:
        resp = requests.post(
            active_sequencer.node_rpc_url,
            json=make_rpc_payload(
                method="admin_stopSequencer",
                params=[],
            ),
        )
        resp.raise_for_status()
        if "error" in resp.json():
            raise Exception(resp.json()["error"])
        typer.echo(
            f"Successfully halted the active sequencer: {active_sequencer.sequencer_id}"
        )
    except Exception as e:
        print_warn(
            f"Failed to halt the active sequencer: {active_sequencer.sequencer_id}"
        )


@app.command()
def force_active_sequencer(network: str, sequencer_id: str, force: bool = False):
    """Forces a sequencer to become active using stop/start."""
    network_obj = get_network(network)
    sequencer = network_obj.get_sequencer_by_id(sequencer_id)
    if sequencer is None:
        typer.echo(f"sequencer ID {sequencer_id} not found in network {network}")
        raise typer.Exit(code=1)

    # Pre-flight check: Ensure all conductors are paused
    sequencers = network_obj.sequencers
    all_paused = all(not sequencer.conductor_active for sequencer in sequencers)
    if not all_paused and not force:
        print_error("Not all conductors are paused. Run 'pause' command first.")
        raise typer.Exit(code=1)

    hash = sequencer.unsafe_l2_hash

    active_sequencer = network_obj.find_active_sequencer()
    if active_sequencer:
      typer.echo(f"Stopping {active_sequencer.sequencer_id}")
      resp = requests.post(
          active_sequencer.node_rpc_url,
          json=make_rpc_payload("admin_stopSequencer", params=[]),
      )
      resp.raise_for_status()
      if "error" in resp.json():
          typer.echo(f"Failed to stop {active_sequencer.sequencer_id}: {resp.json()['error']}")
          raise typer.Exit(code=1)
      hash = resp.json()["result"]

    if not hash:
        typer.echo(f"Failed to get a hash to start sequencer")
        raise typer.Exit(code=1)

    # sleep for a bit to allow sequencer to catch up
    time.sleep(1)

    # start sequencer
    typer.echo(f"Starting {sequencer_id} with hash {hash}")
    resp = requests.post(
        sequencer.node_rpc_url,
        json=make_rpc_payload("admin_startSequencer", params=[hash]),
    )
    resp.raise_for_status()
    if "error" in resp.json():
        typer.echo(f"Failed to start {sequencer_id}: {resp.json()['error']}")
        raise typer.Exit(code=1)

    typer.echo(f"Successfully forced {sequencer_id} to become active")

if __name__ == "__main__":
    app()
