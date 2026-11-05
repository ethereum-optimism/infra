import io
import unittest
from unittest.mock import Mock, patch

import requests
from rich.console import Console
from typer.testing import CliRunner

from op_conductor_ops import cli
from op_conductor_ops.network import Network
from op_conductor_ops.sequencer import Sequencer


class RaftStatusTests(unittest.TestCase):
    def setUp(self):
        self.sequencers = [
            Sequencer(name, f"{name}:50050", f"http://{name}", "http://node", True)
            for name in (
                "source-0",
                "source-1",
                "source-2",
                "dest-0",
                "dest-1",
                "dest-2",
            )
        ]
        self.network = Network("migration", self.sequencers)
        self.members = {
            prefix: [
                {"id": f"{prefix}-{i}", "addr": f"{prefix}-{i}:50050", "suffrage": 0}
                for i in range(3)
            ]
            for prefix in ("source", "dest")
        }
        self.responses = {}
        for sequencer in self.sequencers:
            prefix = sequencer.sequencer_id.split("-")[0]
            self.responses[
                (sequencer.conductor_rpc_url, "conductor_clusterMembership")
            ] = {"result": {"servers": self.members[prefix]}}
            self.responses[(sequencer.conductor_rpc_url, "conductor_leaderWithID")] = {
                "result": self.members[prefix][1]
            }

    def post(self, url, json, timeout):
        self.assertEqual(json["params"], [])
        self.assertEqual(timeout, 10)
        body = self.responses[(url, json["method"])]
        if isinstance(body, Exception):
            raise body
        return Mock(json=Mock(return_value=body))

    def render(self, verbose=True, width=400):
        output = io.StringIO()
        console = Console(file=output, width=width)
        with (
            patch.object(cli, "read_config", return_value=({}, "")),
            patch.object(cli, "get_network", return_value=self.network),
            patch.object(cli.requests, "post", side_effect=self.post) as post,
            patch.object(cli, "console", console),
            patch.object(console, "print", wraps=console.print) as printing,
        ):
            args = ["status", "migration"]
            if verbose:
                args.append("--verbose")
            result = CliRunner().invoke(cli.app, args)
        self.assertEqual(result.exit_code, 0, result.exception)
        if verbose:
            self.assertEqual(post.call_count, len(self.network.sequencers) * 2)
            table = printing.call_args_list[1].args[0]
            rows = list(zip(*(column._cells for column in table.columns)))
            return output.getvalue(), [[str(cell) for cell in row] for row in rows]
        self.assertEqual(post.call_count, 0)
        return output.getvalue(), []

    def test_two_clusters_and_unknown_leader(self):
        self.responses[("http://source-0", "conductor_leaderWithID")] = {
            "result": {"id": "", "addr": "", "suffrage": 0}
        }
        _, rows = self.render()
        self.assertEqual(rows[0][2], "3/6")
        self.assertEqual(rows[0][4], "Unknown")
        self.assertEqual(rows[1][4], "2/6")
        self.assertEqual(rows[3][4], "3/6")
        self.assertIn("Leader unknown", rows[0][5])

    def test_order_does_not_change_agreement(self):
        self.network.sequencers = self.sequencers[:3]
        self.responses[("http://source-2", "conductor_clusterMembership")] = {
            "result": {"servers": list(reversed(self.members["source"]))}
        }
        _, rows = self.render()
        self.assertTrue(all(row[2] == "3/3" and row[4] == "3/3" for row in rows))

    def test_conflicting_leaders(self):
        self.network.sequencers = self.sequencers[:3]
        self.responses[("http://source-0", "conductor_leaderWithID")] = {
            "result": self.members["source"][0]
        }
        _, rows = self.render()
        self.assertEqual(rows[0][4], "1/3")
        self.assertEqual(rows[1][4], "2/3")
        self.assertEqual(rows[2][4], "2/3")

    def test_join_in_progress(self):
        self.network.sequencers = self.sequencers[:3] + [self.sequencers[3]]
        new_member = {"id": "dest-0", "addr": "dest-0:50050", "suffrage": 1}
        for sequencer in self.network.sequencers:
            members = self.members["source"]
            if sequencer.sequencer_id != "source-2":
                members = members + [new_member]
            self.responses[
                (sequencer.conductor_rpc_url, "conductor_clusterMembership")
            ] = {"result": {"servers": members}}
            self.responses[(sequencer.conductor_rpc_url, "conductor_leaderWithID")] = {
                "result": self.members["source"][1]
            }
        _, rows = self.render()
        self.assertEqual([row[2] for row in rows], ["3/4", "3/4", "1/4", "3/4"])
        self.assertTrue(all(row[4] == "4/4" for row in rows))
        self.assertIn("dest-0 (Nonvoter)", rows[3][1])

    def test_empty_candidate_is_not_joined(self):
        self.responses[("http://dest-0", "conductor_clusterMembership")] = {
            "result": {"servers": []}
        }
        self.responses[("http://dest-0", "conductor_leaderWithID")] = {"result": None}
        _, rows = self.render()
        self.assertEqual(rows[3][2], "1/6")
        self.assertEqual(rows[3][4], "Unknown")
        self.assertIn("Empty membership; not joined", rows[3][5])
        self.assertNotIn("check before join", rows[3][5])

    def test_null_servers_is_empty_membership(self):
        self.responses[("http://dest-0", "conductor_clusterMembership")] = {
            "result": {"servers": None, "version": 0}
        }
        self.responses[("http://dest-0", "conductor_leaderWithID")] = {
            "result": {"id": "", "addr": "", "suffrage": 0}
        }
        _, rows = self.render()
        self.assertEqual(rows[3][1], "Empty")
        self.assertEqual(rows[3][2], "1/6")
        self.assertEqual(rows[3][4], "Unknown")
        self.assertIn("Empty membership; not joined", rows[3][5])

    def test_rpc_errors_do_not_count_as_agreement(self):
        for sequencer in self.sequencers[:2]:
            self.responses[
                (sequencer.conductor_rpc_url, "conductor_clusterMembership")
            ] = {"error": {"code": -32601, "message": "Method not found"}}
            self.responses[(sequencer.conductor_rpc_url, "conductor_leaderWithID")] = (
                requests.Timeout("RPC timed out")
            )
        _, rows = self.render()
        self.assertEqual(rows[0][2], "Unknown")
        self.assertEqual(rows[1][2], "Unknown")
        self.assertEqual(rows[2][2], "1/6")
        self.assertEqual(rows[2][4], "1/6")
        self.assertIn("Method not found", rows[0][5])
        self.assertIn("RPC timed out", rows[0][5])

    def test_addresses_and_roles_affect_agreement(self):
        self.network.sequencers = self.sequencers[:3]
        changed = [dict(member) for member in self.members["source"]]
        changed[0]["addr"] = "other:50050"
        self.responses[("http://source-0", "conductor_clusterMembership")] = {
            "result": {"servers": changed}
        }
        changed_roles = [dict(member) for member in self.members["source"]]
        changed_roles[0]["suffrage"] = 1
        self.responses[("http://source-2", "conductor_clusterMembership")] = {
            "result": {"servers": changed_roles}
        }
        self.responses[("http://source-2", "conductor_leaderWithID")] = {
            "result": {**self.members["source"][1], "addr": "other-leader:50050"}
        }
        _, rows = self.render()
        self.assertTrue(all(row[2] == "1/3" for row in rows))
        self.assertEqual([row[4] for row in rows], ["2/3", "2/3", "1/3"])

    def test_plain_status_stays_compact(self):
        output, _ = self.render(verbose=False)
        self.assertNotIn("Raft agreement:", output)
        self.assertIn("Sequencer ID", output)

    def test_raft_values_wrap_without_truncation(self):
        output, _ = self.render(width=120)
        raft_output = output[output.index("Raft agreement:") :]
        self.assertNotIn("…", raft_output)


if __name__ == "__main__":
    unittest.main()
