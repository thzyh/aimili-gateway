import sqlite3
import unittest
from unittest.mock import patch

import provision


class RecoveryTests(unittest.TestCase):
    def database(self, inbound_id=0):
        db = sqlite3.connect(":memory:")
        db.execute("CREATE TABLE proxy_groups (id TEXT, aimili_slot INTEGER, status TEXT, public_inbound_id INTEGER, mixed_inbound_id INTEGER)")
        db.execute("INSERT INTO proxy_groups VALUES ('agw-test', 0, 'provisioning', ?, 0)", (inbound_id,))
        db.execute("INSERT INTO proxy_groups VALUES ('agw-ready', 1, 'ready', 1, 2)")
        db.commit()
        return db

    def test_only_incomplete_installation_record_is_removed(self):
        db = self.database()
        requests = []

        class Client:
            def __init__(self, origin, credentials):
                pass

            def login(self):
                pass

            def request(self, method, path):
                requests.append((method, path))

        with patch.object(provision.sqlite3, "connect", return_value=db), patch.object(provision, "GatewayClient", Client):
            provision.recover_interrupted_slot_adoptions("https://example.test", 4, {})
        self.assertEqual(requests, [("DELETE", "/api/v1/proxy-groups/agw-test")])

    def test_record_with_saved_inbound_id_requires_manual_review(self):
        db = self.database(inbound_id=17)
        with patch.object(provision.sqlite3, "connect", return_value=db):
            with self.assertRaisesRegex(RuntimeError, "无法安全自动恢复"):
                provision.recover_interrupted_slot_adoptions("https://example.test", 4, {})


if __name__ == "__main__":
    unittest.main()
