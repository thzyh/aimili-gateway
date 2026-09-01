import argparse
import json
import socket
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


def request_json(url, method="GET", body=None):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read())


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
            return self.reply(200, state["main"])
        if self.server.role == "aimili" and self.path == "/control/v1/candidates":
            return self.reply(200, {"candidateIds": [item for item in state["candidates"] if item not in state["excluded"]]})
        if self.server.role == "xui" and self.path == "/panel/api/clients/gateway/inboundAliases":
            return self.reply(200, {"aliases": state["aliases"]})
        if self.server.role == "gateway" and self.path == "/state":
            return self.reply(200, state)
        return self.reply(404, {"errorCode": "not_found"})

    def do_POST(self):
        state = read_json(self.server.state_path)
        body = self.body()
        if self.server.role == "aimili" and self.path == "/control/v1/slots/1/assign":
            if body.get("candidateId") == "candidate-bad-kr":
                state["excluded"].append("candidate-bad-kr")
                write_json(self.server.state_path, state)
                return self.reply(409, {"errorCode": "candidate_dial_failed", "candidateRejected": True})
            state["slot1"] = {"candidateId": "candidate-kr", "country": "韩国", "publicPort": 20000}
            write_json(self.server.state_path, state)
            return self.reply(200, state["slot1"])
        if self.server.role == "xui" and self.path == "/panel/api/clients/gateway/inboundAliases":
            state["aliases"].update(body["aliases"])
            write_json(self.server.state_path, state)
            return self.reply(200, {"aliases": state["aliases"]})
        if self.server.role == "gateway":
            if self.path == "/transactions/protocol-switch":
                _, main = request_json(self.server.aimili + "/control/v1/main")
                state["main"] = main
                write_json(self.server.state_path, state)
                return self.reply(200, {"scenario": "old_main_protocol_switch", "role": "main", "countryCode": "JP", "country": main["country"], "publicPort": 8443, "result": "ready"})
            if self.path == "/transactions/country-replace":
                status, slot = request_json(self.server.aimili + "/control/v1/slots/1/assign", "POST", {"candidateId": "candidate-kr"})
                if status != 200:
                    return self.reply(409, slot)
                alias = "出口位 1_" + slot["country"]
                request_json(self.server.xui + "/panel/api/clients/gateway/inboundAliases", "POST", {"aliases": {"slot1": alias}})
                state["slot1"] = slot
                write_json(self.server.state_path, state)
                return self.reply(200, {"scenario": "candidate_country_replace", "role": "slot-1", "countryCode": "KR", "country": slot["country"], "publicPort": slot["publicPort"], "alias": alias, "aliasUpdated": alias == "出口位 1_韩国", "result": "ready"})
            if self.path == "/transactions/failed-replace":
                previous = dict(state["slot1"])
                _, aliases_before = request_json(self.server.xui + "/panel/api/clients/gateway/inboundAliases")
                status, failure = request_json(self.server.aimili + "/control/v1/slots/1/assign", "POST", {"candidateId": "candidate-bad-kr"})
                _, candidates = request_json(self.server.aimili + "/control/v1/candidates")
                state["slot1"] = previous
                write_json(self.server.state_path, state)
                _, aliases_after = request_json(self.server.xui + "/panel/api/clients/gateway/inboundAliases")
                _, gateway_after = request_json("http://127.0.0.1:" + str(self.server.server_port) + "/state")
                return self.reply(200, {"scenario": "failed_replace", "role": "slot-1", "countryCode": "KR", "country": previous["country"], "publicPort": previous["publicPort"], "errorCode": failure["errorCode"], "candidateExcluded": status == 409 and "candidate-bad-kr" not in candidates["candidateIds"], "gatewayStatePreserved": gateway_after["slot1"] == previous, "aliasesPreserved": aliases_after == aliases_before, "transaction": "rolled_back", "result": "rolled_back"})
        return self.reply(404, {"errorCode": "not_found"})


def serve(args):
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.role, server.state_path = args.service, args.state
    server.aimili, server.xui = args.aimili, args.xui
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


def orchestrate():
    with tempfile.TemporaryDirectory(prefix="egress-ux-process-") as root:
        paths = {name: str(Path(root) / f"{name}.json") for name in ("aimili", "xui", "gateway")}
        write_json(paths["aimili"], {"main": {"candidateId": "candidate-main-jp", "country": "日本"}, "slot1": {"candidateId": "candidate-jp", "country": "日本", "publicPort": 20000}, "candidates": ["candidate-kr", "candidate-bad-kr"], "excluded": []})
        write_json(paths["xui"], {"aliases": {"main": "主连接_日本", "slot1": "出口位 1_日本"}})
        write_json(paths["gateway"], {"main": {"candidateId": "stale-main", "country": "日本"}, "slot1": {"candidateId": "candidate-jp", "country": "日本", "publicPort": 20000}})
        ports = {name: free_port() for name in paths}
        urls = {name: f"http://127.0.0.1:{port}" for name, port in ports.items()}
        processes = {}
        try:
            for name in ("aimili", "xui", "gateway"):
                command = [sys.executable, __file__, "--service", name, "--port", str(ports[name]), "--state", paths[name], "--aimili", urls["aimili"], "--xui", urls["xui"]]
                processes[name] = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                wait_ready(urls[name])
            transactions = []
            for path in ("/transactions/protocol-switch", "/transactions/country-replace", "/transactions/failed-replace"):
                status, transaction = request_json(urls["gateway"] + path, "POST", {})
                if status != 200:
                    raise RuntimeError(f"fixture transaction failed: {path}")
                transactions.append(transaction)
            if transactions[0]["result"] != "ready" or transactions[0]["country"] != "日本":
                raise RuntimeError("main identity was not synchronized before protocol switch")
            if transactions[1]["alias"] != "出口位 1_韩国":
                raise RuntimeError("target subscription alias did not follow the replacement country")
            failure = transactions[2]
            if not all((failure["candidateExcluded"], failure["gatewayStatePreserved"], failure["aliasesPreserved"], failure["transaction"] == "rolled_back")):
                raise RuntimeError("failed replacement did not preserve the committed transaction state")
            print(json.dumps({"services": ["aimili", "xui", "gateway"], "transactions": transactions}, ensure_ascii=False))
        finally:
            for process in processes.values():
                process.terminate()
            for process in processes.values():
                process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--service", choices=("aimili", "xui", "gateway"))
    parser.add_argument("--port", type=int)
    parser.add_argument("--state")
    parser.add_argument("--aimili")
    parser.add_argument("--xui")
    args = parser.parse_args()
    serve(args) if args.service else orchestrate()


if __name__ == "__main__":
    main()
