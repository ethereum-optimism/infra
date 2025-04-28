import concurrent.futures

import requests
import typer

from utils import make_rpc_payload


class Sequencer:
    def __init__(
        self,
        sequencer_id,
        raft_addr,
        conductor_rpc_url,
        node_rpc_url,
        voting,
        rollup_boost_rpc_url=None,
        builder_rpc_url=None,
    ):
        self.sequencer_id = sequencer_id
        self.raft_addr = raft_addr
        self.conductor_rpc_url = conductor_rpc_url
        self.node_rpc_url = node_rpc_url
        self.rollup_boost_rpc_url = rollup_boost_rpc_url
        self.builder_rpc_url = builder_rpc_url
        self.voting = voting
        self.conductor_active = None
        self.conductor_leader = None
        self.sequencer_healthy = None
        self.sequencer_active = None
        self.unsafe_l2_hash = None
        self.unsafe_l2_number = None
        self.builder_unsafe_l2_number = None
        self.builder_unsafe_l2_hash = None
        self.update_successful = False
        self.rollup_boost_execution_mode = None

    def _get_sequencer_active(self):
        resp = requests.post(
            self.node_rpc_url,
            json=make_rpc_payload("admin_sequencerActive"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.sequencer_active = resp.json()["result"]

    def _get_sequencer_healthy(self):
        resp = requests.post(
            self.conductor_rpc_url,
            json=make_rpc_payload("conductor_sequencerHealthy"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.sequencer_healthy = resp.json()["result"]

    def _get_conductor_active(self):
        resp = requests.post(
            self.conductor_rpc_url,
            json=make_rpc_payload("conductor_active"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.conductor_active = resp.json()["result"]

    def _get_conductor_leader(self):
        resp = requests.post(
            self.conductor_rpc_url,
            json=make_rpc_payload("conductor_leader"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.conductor_leader = resp.json()["result"]

    def _get_rollup_boost_execution_mode(self):
        resp = requests.post(
            self.rollup_boost_rpc_url,
            json=make_rpc_payload("debug_getExecutionMode"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        self.rollup_boost_execution_mode = resp.json()["result"]["execution_mode"]

    def _get_builder_unsafe_l2(self):
        resp = requests.post(
            self.builder_rpc_url,
            json=make_rpc_payload("optimism_syncStatus"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        result = resp.json()["result"]
        self.builder_unsafe_l2_number = result["unsafe_l2"]["number"]
        self.builder_unsafe_l2_hash = result["unsafe_l2"]["hash"]
        print(
            f"{self.sequencer_id} getting builder unsafe_l2: {self.builder_unsafe_l2_number}"
        )

    def _get_unsafe_l2(self):
        resp = requests.post(
            self.node_rpc_url,
            json=make_rpc_payload("optimism_syncStatus"),
        )
        try:
            resp.raise_for_status()
        except Exception as e:
            return None
        result = resp.json()["result"]
        self.unsafe_l2_number = result["unsafe_l2"]["number"]
        self.unsafe_l2_hash = result["unsafe_l2"]["hash"]

    def cluster_membership(self):
        resp = requests.post(
            self.conductor_rpc_url,
            json=make_rpc_payload("conductor_clusterMembership"),
        )
        resp.raise_for_status()
        return resp.json()["result"]["servers"]

    def update(self):
        functions = [
            self._get_conductor_active,
            self._get_conductor_leader,
            self._get_sequencer_healthy,
            self._get_sequencer_active,
            self._get_unsafe_l2,
        ]

        if self.builder_rpc_url:
            functions.append(self._get_builder_unsafe_l2)
        if self.rollup_boost_rpc_url:
            functions.append(self._get_rollup_boost_execution_mode)

        with concurrent.futures.ThreadPoolExecutor() as executor:
            futures = {executor.submit(func): func for func in functions}
            for future in concurrent.futures.as_completed(futures):
                func = futures[future]
                try:
                    result = future.result()
                except Exception as e:
                    typer.echo(f"{func.__name__} raised an exception: {e}")
                    self.update_successful = False
                else:
                    self.update_successful = True
