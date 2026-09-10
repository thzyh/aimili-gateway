import http.client
import json
import threading
import time
import unittest
from unittest import mock

import vpngate_manager as manager


class NativeWebUITests(unittest.TestCase):
    def setUp(self):
        self.config = mock.patch.object(
            manager,
            "load_ui_config",
            return_value={"secret_path": "qa", "username": "qa", "password": "test"},
        )
        self.config.start()
        manager.active_sessions["test-session"] = time.time() + 60
        self.server = manager.DualStackHTTPServer(("127.0.0.1", 0), manager.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(2)
        manager.active_sessions.pop("test-session", None)
        self.config.stop()

    def request(self, method, path, payload=None):
        body = b"" if payload is None else json.dumps(payload).encode("utf-8")
        headers = {"Cookie": "session=test-session"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
            headers["Content-Length"] = str(len(body))
        connection = http.client.HTTPConnection(*self.server.server_address, timeout=2)
        try:
            connection.request(method, path, body=body, headers=headers)
            response = connection.getresponse()
            document = json.loads(response.read().decode("utf-8"))
            return response.status, document
        finally:
            connection.close()

    def test_nodes_endpoint_hides_unavailable_nodes_and_exposes_counts(self):
        nodes = [
            {"id": "jp-good", "country_short": "JP", "probe_status": "available"},
            {"id": "us-bad", "country_short": "US", "probe_status": "unavailable"},
        ]
        catalog = [
            {"code": "JP", "name": "日本", "candidateCount": 12, "observedAt": 100.0},
            {"code": "US", "name": "美国", "candidateCount": 8, "observedAt": 100.0},
        ]
        with (
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "get_state", return_value={}),
            mock.patch.object(manager, "country_catalog_snapshot", return_value=catalog),
            mock.patch.object(manager, "country_refresh_snapshot", return_value={"state": "idle"}),
        ):
            status, document = self.request("GET", "/qa/api/nodes")

        self.assertEqual(status, 200)
        self.assertEqual([item["id"] for item in document["nodes"]], ["jp-good"])
        self.assertEqual(document["node_stats"]["official_node_count"], 20)
        self.assertEqual(document["node_stats"]["current_node_count"], 1)

    def test_country_refresh_endpoint_normalizes_and_starts_background_job(self):
        with mock.patch.object(
            manager,
            "start_country_refresh",
            return_value={"state": "running", "country": "JP"},
        ) as start:
            status, document = self.request(
                "POST", "/qa/api/country_refresh", {"country": "jp"}
            )

        self.assertEqual(status, 202)
        self.assertTrue(document["ok"])
        start.assert_called_once_with("JP")

    def test_country_refresh_endpoint_rejects_invalid_country(self):
        status, document = self.request(
            "POST", "/qa/api/country_refresh", {"country": "Japan"}
        )

        self.assertEqual(status, 400)
        self.assertEqual(document["error_code"], "invalid_country")


if __name__ == "__main__":
    unittest.main()
