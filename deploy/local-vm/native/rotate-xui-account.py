#!/usr/bin/env python3
"""Rotate a local 3x-ui account without exposing credentials in argv/env/output."""
import json
import sys
import urllib.parse
import urllib.request
from http.cookiejar import CookieJar


def main() -> int:
    payload = json.load(sys.stdin)
    required = ("baseUrl", "oldUsername", "oldPassword", "newUsername", "newPassword")
    if any(not isinstance(payload.get(key), str) or not payload[key] for key in required):
        return 2
    base = payload["baseUrl"].rstrip("/") + "/"
    jar = CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def call(path: str, body=None, csrf: str = ""):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(urllib.parse.urljoin(base, path), data=data)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        if csrf:
            request.add_header("X-CSRF-Token", csrf)
        with opener.open(request, timeout=10) as response:
            return json.loads(response.read().decode()), response.headers.get("X-CSRF-Token", "")

    csrf_response, _ = call("csrf-token")
    if not isinstance(csrf_response, dict) or csrf_response.get("success") is not True:
        return 3
    csrf = csrf_response.get("obj")
    if not isinstance(csrf, str) or not csrf:
        return 3
    login_body = {"username": payload["oldUsername"], "password": payload["oldPassword"], "twoFactorCode": ""}
    login, _ = call("login", login_body, csrf)
    if isinstance(login, dict) and login.get("success") is True:
        updated, _ = call("panel/api/setting/updateUser", {
            "oldUsername": payload["oldUsername"],
            "oldPassword": payload["oldPassword"],
            "newUsername": payload["newUsername"],
            "newPassword": payload["newPassword"],
            "twoFactorCode": "",
        }, csrf)
        if not isinstance(updated, dict) or updated.get("success") is not True:
            return 5
        return 0

    # A process may have completed the API update before it was interrupted
    # while committing the pending credentials file. Accept the new account on
    # a retry, but never pass either account through argv or environment.
    rotated, _ = call("login", {"username": payload["newUsername"], "password": payload["newPassword"], "twoFactorCode": ""}, csrf)
    if not isinstance(rotated, dict) or rotated.get("success") is not True:
        return 4
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
