import argparse
import http.cookiejar
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def read_json(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def write_json(path, value):
    Path(path).write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")


def request_json(url, method="GET", body=None, opener=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    request_headers = {"Content-Type": "application/json"}
    request_headers.update(headers or {})
    request = urllib.request.Request(url, data=data, method=method, headers=request_headers)
    client = opener.open if opener is not None else urllib.request.urlopen
    try:
        with client(request, timeout=5) as response:
            payload = response.read()
            return response.status, json.loads(payload) if payload else {}
    except urllib.error.HTTPError as exc:
        payload = exc.read()
        return exc.code, json.loads(payload) if payload else {}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, status, value):
        payload = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def body(self):
        return json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")

    def do_GET(self):
        if self.path == "/health":
            return self.reply(200, {"status": "ready"})
        state = read_json(self.server.state_path)
        if self.server.role == "aimili" and self.path == "/control/v1/main":
            state["mainReads"] += 1
            write_json(self.server.state_path, state)
            return self.reply(200, state["main"])
        if self.server.role == "aimili" and self.path == "/control/v1/candidates":
            return self.reply(200, {"candidateIds": [item for item in state["candidates"] if item not in state["excluded"]]})
        if self.server.role == "aimili" and self.path == "/fixture/state":
            return self.reply(200, {"mainReads": state["mainReads"]})
        if self.server.role == "xui" and self.path == "/panel/api/clients/gateway/inboundAliases":
            return self.reply(200, {"aliases": state["aliases"]})
        return self.reply(404, {"errorCode": "not_found"})

    def do_POST(self):
        state = read_json(self.server.state_path)
        body = self.body()
        if self.server.role == "aimili" and self.path == "/control/v1/slots/1/assign":
            if body.get("candidateId") == "candidate-bad-kr":
                if "candidate-bad-kr" not in state["excluded"]:
                    state["excluded"].append("candidate-bad-kr")
                write_json(self.server.state_path, state)
                return self.reply(409, {"errorCode": "candidate_dial_failed", "candidateRejected": True})
            if body.get("candidateId") != "candidate-kr":
                return self.reply(404, {"errorCode": "candidate_not_found"})
            state["slot1"] = {"candidateId": "candidate-kr", "country": "韩国", "publicPort": 20000}
            write_json(self.server.state_path, state)
            return self.reply(200, state["slot1"])
        if self.server.role == "xui" and self.path == "/panel/api/clients/gateway/inboundAliases":
            aliases = body.get("aliases")
            if not isinstance(aliases, dict):
                return self.reply(400, {"errorCode": "invalid_request"})
            state["aliases"].update(aliases)
            write_json(self.server.state_path, state)
            return self.reply(200, {"aliases": state["aliases"]})
        return self.reply(404, {"errorCode": "not_found"})


def serve(args):
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.role, server.state_path = args.service, args.state
    server.serve_forever()


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_ready(url):
    for _ in range(100):
        try:
            if request_json(url + "/health")[0] == 200:
                return
        except OSError:
            time.sleep(0.05)
    raise RuntimeError("fixture process did not become ready")


def wait_gateway_ready(path, process):
    for _ in range(200):
        if Path(path).is_file():
            return read_json(path)
        if process.poll() is not None:
            raise RuntimeError("Gateway fixture process exited before becoming ready")
        time.sleep(0.05)
    raise RuntimeError("Gateway fixture process did not become ready")


def require(status, expected, label):
    if status != expected:
        raise RuntimeError(f"{label} returned HTTP {status}, expected {expected}")


def group_by_id(groups, target):
    return next((group for group in groups if group.get("id") == target), None)


def orchestrate(gateway_binary):
    with tempfile.TemporaryDirectory(prefix="egress-ux-process-") as root:
        paths = {name: str(Path(root) / f"{name}.json") for name in ("aimili", "xui")}
        write_json(paths["aimili"], {"main": {"candidateId": "candidate-main-jp", "country": "日本"}, "mainReads": 0, "slot1": {"candidateId": "candidate-jp", "country": "日本", "publicPort": 20000}, "candidates": ["candidate-kr", "candidate-bad-kr"], "excluded": []})
        write_json(paths["xui"], {"aliases": {"main": "主连接_日本", "slot1": "出口位 1_日本"}})
        ports = {name: free_port() for name in paths}
        urls = {name: f"http://127.0.0.1:{port}" for name, port in ports.items()}
        ready_path = str(Path(root) / "gateway-ready.json")
        stop_path = str(Path(root) / "gateway-stop")
        processes = {}
        try:
            for name in ("aimili", "xui"):
                command = [sys.executable, __file__, "--service", name, "--port", str(ports[name]), "--state", paths[name]]
                processes[name] = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                wait_ready(urls[name])
            environment = os.environ.copy()
            environment.update({
                "AIMILI_EGRESS_PROCESS_HELPER": "1",
                "AIMILI_EGRESS_AIMILI_URL": urls["aimili"],
                "AIMILI_EGRESS_XUI_URL": urls["xui"],
                "AIMILI_EGRESS_READY_FILE": ready_path,
                "AIMILI_EGRESS_STOP_FILE": stop_path,
            })
            processes["gateway"] = subprocess.Popen([gateway_binary, "-test.run=^TestEgressProcessGatewayHelper$"], env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            gateway = wait_gateway_ready(ready_path, processes["gateway"])
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
            status, _ = request_json(gateway["url"] + "/api/v1/auth/login", "POST", {"username": "owner", "password": "local-only-test-password", "totp": "287082"}, opener, {"Origin": gateway["origin"]})
            require(status, 204, "Gateway login")
            status, session = request_json(gateway["url"] + "/api/v1/auth/session", opener=opener)
            require(status, 200, "Gateway session")
            mutation_headers = {"Origin": gateway["origin"], "X-CSRF-Token": session["csrfToken"]}

            status, protocol = request_json(gateway["url"] + "/api/v1/proxy-groups/agw-main/protocol-mode", "PUT", {"protocolMode": "vless_xhttp_reality"}, opener, dict(mutation_headers, **{"Idempotency-Key": "fixture-main-protocol"}))
            require(status, 200, "main protocol switch")
            status, aimili_state = request_json(urls["aimili"] + "/fixture/state")
            require(status, 200, "AimiliVPN main readback")
            if aimili_state["mainReads"] != 1 or protocol.get("protocolMode") != "vless_xhttp_reality":
                raise RuntimeError("Gateway did not read the current main identity before switching protocol")

            status, replaced = request_json(gateway["url"] + "/api/v1/proxy-groups/candidate-kr/replace", "POST", {"targetGroupId": "agw-slot-1"}, opener, dict(mutation_headers, **{"Idempotency-Key": "fixture-country-replace"}))
            require(status, 200, "country replacement")
            status, aliases = request_json(urls["xui"] + "/panel/api/clients/gateway/inboundAliases")
            require(status, 200, "x-ui alias readback")
            if replaced.get("countryCode") != "KR" or aliases["aliases"] != {"main": "主连接_日本", "slot1": "出口位 1_韩国"}:
                raise RuntimeError("only the target subscription alias must follow the replacement country")

            status, groups_before = request_json(gateway["url"] + "/api/v1/proxy-groups", opener=opener)
            require(status, 200, "Gateway state before failed replacement")
            aliases_before = aliases
            status, failure = request_json(gateway["url"] + "/api/v1/proxy-groups/candidate-bad-kr/replace", "POST", {"targetGroupId": "agw-slot-1"}, opener, dict(mutation_headers, **{"Idempotency-Key": "fixture-failed-replace"}))
            require(status, 409, "failed replacement")
            status, candidates = request_json(urls["aimili"] + "/control/v1/candidates")
            require(status, 200, "AimiliVPN candidate readback")
            status, groups_after = request_json(gateway["url"] + "/api/v1/proxy-groups", opener=opener)
            require(status, 200, "Gateway state after failed replacement")
            status, aliases_after = request_json(urls["xui"] + "/panel/api/clients/gateway/inboundAliases")
            require(status, 200, "x-ui alias rollback readback")
            gateway_preserved = group_by_id(groups_before, "agw-slot-1") == group_by_id(groups_after, "agw-slot-1")
            aliases_preserved = aliases_before == aliases_after
            candidate_excluded = "candidate-bad-kr" not in candidates["candidateIds"]
            if failure.get("error") != "candidate_dial_failed" or not all((gateway_preserved, aliases_preserved, candidate_excluded)):
                raise RuntimeError("failed replacement did not preserve the committed state")

            transactions = [
                {"scenario": "old_main_protocol_switch", "role": "main", "countryCode": "JP", "result": "ready"},
                {"scenario": "candidate_country_replace", "role": "slot-1", "countryCode": replaced["countryCode"], "result": "ready", "aliasUpdated": True},
                {"scenario": "failed_replace", "role": "slot-1", "countryCode": "KR", "result": "rolled_back", "errorCode": failure["error"], "candidateExcluded": candidate_excluded, "gatewayStatePreserved": gateway_preserved, "aliasesPreserved": aliases_preserved},
            ]
            print(json.dumps({"services": ["aimili-fixture", "xui-fixture", "gateway-test-binary"], "transactions": transactions}, ensure_ascii=False))
        finally:
            Path(stop_path).touch()
            gateway_process = processes.get("gateway")
            if gateway_process is not None:
                try:
                    gateway_process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    gateway_process.terminate()
                    gateway_process.wait(timeout=5)
            for name in ("aimili", "xui"):
                process = processes.get(name)
                if process is not None:
                    process.terminate()
                    process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--service", choices=("aimili", "xui"))
    parser.add_argument("--port", type=int)
    parser.add_argument("--state")
    parser.add_argument("--gateway-binary")
    args = parser.parse_args()
    if args.service:
        serve(args)
    elif args.gateway_binary:
        orchestrate(args.gateway_binary)
    else:
        parser.error("--gateway-binary is required")


if __name__ == "__main__":
    main()
