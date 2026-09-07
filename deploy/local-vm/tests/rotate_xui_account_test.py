#!/usr/bin/env python3
import http.server
import json
import pathlib
import subprocess
import threading
import urllib.parse


HELPER = pathlib.Path(__file__).parents[1] / "native" / "rotate-xui-account.py"


class Handler(http.server.BaseHTTPRequestHandler):
    password = "new-password"
    username = "new-user"
    old_password = "admin"
    old_username = "admin"
    calls = []

    def log_message(self, *_args):
        return

    def _json(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Set-Cookie", "session=test")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        assert self.path == "/xui/csrf-token"
        self.calls.append(("csrf", None))
        self._json({"success": True, "obj": "csrf-token"})

    def do_POST(self):
        length = int(self.headers["Content-Length"])
        payload = json.loads(self.rfile.read(length))
        self.calls.append((self.path, payload))
        if self.path == "/xui/login":
            success = (payload["username"], payload["password"]) in {
                (self.old_username, self.old_password),
                (self.username, self.password),
            }
            self._json({"success": success})
            return
        assert self.path == "/xui/panel/api/setting/updateUser"
        assert payload["oldUsername"] == self.old_username
        assert payload["oldPassword"] == self.old_password
        self._json({"success": True})


def run(payload):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        result = subprocess.run(
            ["python3", str(HELPER)],
            input=json.dumps(payload).encode(),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        return result
    finally:
        server.shutdown()
        thread.join()


def main():
    payload = {
        "baseUrl": "http://127.0.0.1:%d/xui/" % 0,
        "oldUsername": "admin",
        "oldPassword": "admin",
        "newUsername": "new-user",
        "newPassword": "new-password",
    }
    # Replace the port in the payload with the ephemeral server port in a
    # small inline server wrapper so this test exercises the real helper.
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        payload["baseUrl"] = "http://127.0.0.1:%d/xui/" % server.server_port
        result = subprocess.run(
            ["python3", str(HELPER)],
            input=json.dumps(payload).encode(),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        assert result.returncode == 0, result.stderr.decode()
        paths = [path for path, _ in Handler.calls]
        assert paths == ["csrf", "/xui/login", "/xui/panel/api/setting/updateUser"]
    finally:
        server.shutdown()
        thread.join()

    Handler.calls.clear()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        payload["baseUrl"] = "http://127.0.0.1:%d/xui/" % server.server_port
        Handler.old_username = "rotated"
        Handler.old_password = "rotated-password"
        result = subprocess.run(
            ["python3", str(HELPER)],
            input=json.dumps(payload).encode(),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        assert result.returncode == 0, result.stderr.decode()
        assert [path for path, _ in Handler.calls] == ["csrf", "/xui/login", "/xui/login"]
    finally:
        server.shutdown()
        thread.join()


if __name__ == "__main__":
    main()
