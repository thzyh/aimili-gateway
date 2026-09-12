import importlib.util
import pathlib
import sqlite3
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("set-capacity-safe-remote.py")
SPEC = importlib.util.spec_from_file_location("set_capacity_safe_remote", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class CapacityConfigTests(unittest.TestCase):
    def test_online_database_backup_contains_committed_rows(self):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "source.db"
            destination = pathlib.Path(directory) / "backup.db"
            database = sqlite3.connect(source)
            try:
                database.execute("pragma journal_mode=wal")
                database.execute("create table sample(value text not null)")
                database.execute("insert into sample(value) values ('committed')")
                database.commit()
            finally:
                database.close()
            MODULE.backup_database(source, destination)
            database = sqlite3.connect(destination)
            try:
                self.assertEqual("committed", database.execute("select value from sample").fetchone()[0])
            finally:
                database.close()

    def test_updates_only_capacity(self):
        original = {"listen": "127.0.0.1:9080", "maxProxyGroups": 1}
        updated = MODULE.updated_config(original, 2)
        self.assertEqual({"listen": "127.0.0.1:9080", "maxProxyGroups": 2}, updated)
        self.assertEqual(1, original["maxProxyGroups"])

    def test_accepts_capacity_four_for_one_step_expansion(self):
        self.assertEqual(4, MODULE.updated_config({"maxProxyGroups": 3}, 4)["maxProxyGroups"])

    def test_rejects_capacity_five(self):
        with self.assertRaisesRegex(ValueError, "1, 2, 3, or 4"):
            MODULE.updated_config({"maxProxyGroups": 1}, 5)


if __name__ == "__main__":
    unittest.main()
