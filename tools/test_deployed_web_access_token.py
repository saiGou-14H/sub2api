#!/usr/bin/env python3
"""Run a disposable Web access-token smoke test against a remote Sub2API host.

Secrets are read from stdin (SSH password, then access token) or from the
environment. They are never written to disk or included in the result.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import sys
from pathlib import Path

import paramiko


DEFAULT_HOST = "66.92.18.39"
DEFAULT_PORT = 3643
DEFAULT_REMOTE_ROOT = "/root/sub2api-deploy-9999"
DEFAULT_FINGERPRINT = "SHA256:LEF7k+LxS5/z1NKdhayg+J+OlhFwo+6mtIl4EHqRvSI"


REMOTE_TEST_CODE = r'''
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
import uuid


BASE = "http://127.0.0.1:9999/api/v1"
WEB_BASE = "http://127.0.0.1:9999"


def parse_env(path):
    values = {}
    for raw in open(path, "r", encoding="utf-8"):
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key.strip()] = value
    return values


def request(url, method="GET", body=None, bearer=None, timeout=35):
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            raw = response.read()
            try:
                parsed = json.loads(raw.decode("utf-8", "replace"))
            except Exception:
                parsed = {"raw": raw.decode("utf-8", "replace")[:1200]}
            return response.status, parsed
    except urllib.error.HTTPError as exc:
        raw = exc.read(2000).decode("utf-8", "replace")
        try:
            parsed = json.loads(raw)
        except Exception:
            parsed = {"raw": raw[:1200]}
        return exc.code, parsed
    except Exception as exc:
        return 0, {"error_type": type(exc).__name__, "error": str(exc)[:1200]}


def unwrap(value):
    if isinstance(value, dict) and isinstance(value.get("data"), (dict, list)):
        return value["data"]
    return value


def find_value(value, names):
    if isinstance(value, dict):
        for name in names:
            if name in value and value[name] not in (None, ""):
                return value[name]
        for child in value.values():
            found = find_value(child, names)
            if found not in (None, ""):
                return found
    elif isinstance(value, list):
        for child in value:
            found = find_value(child, names)
            if found not in (None, ""):
                return found
    return None


def content_text(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        parts = []
        for item in value:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict):
                text = item.get("text") or item.get("value")
                if isinstance(text, str):
                    parts.append(text)
        return "".join(parts)
    if isinstance(value, dict):
        return content_text(value.get("text") or value.get("value") or "")
    return ""


def safe_error(value, secrets):
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    for secret in secrets:
        if secret:
            text = text.replace(secret, "<redacted>")
    text = re.sub(r"(?i)(bearer\s+)[^\s\"}]+", r"\1<redacted>", text)
    return text[:1600]


def main():
    token = sys.stdin.readline().strip()
    if not token:
        print(json.dumps({"ok": False, "stage": "input", "error": "access token is empty"}))
        return 2

    env = parse_env("/root/sub2api-deploy-9999/.env")
    admin_email = env.get("ADMIN_EMAIL", "")
    admin_password = env.get("ADMIN_PASSWORD", "")
    secrets = [token, admin_password]
    result = {
        "ok": False,
        "endpoint": WEB_BASE + "/v1/chat/completions",
        "transport": "web",
        "proxy_configured": False,
    }
    admin_token = ""
    account_id = None
    key_id = None
    gateway_key = ""
    try:
        status, payload = request(
            BASE + "/auth/login",
            "POST",
            {"email": admin_email, "password": admin_password},
        )
        result["login_status"] = status
        login_data = unwrap(payload)
        if status != 200 or not isinstance(login_data, dict):
            result["stage"] = "admin_login"
            result["error"] = safe_error(payload, secrets)
            return 1
        admin_token = str(login_data.get("access_token") or "")
        if not admin_token:
            result["stage"] = "admin_login"
            result["error"] = "login response did not contain an access token"
            return 1

        name = "web-token-smoke-" + uuid.uuid4().hex[:10]
        status, payload = request(
            BASE + "/admin/accounts",
            "POST",
            {
                "name": name,
                "platform": "openai",
                "type": "setup-token",
                "credentials": {"access_token": token},
                "extra": {"openai_transport": "web"},
                "concurrency": 1,
                "priority": 0,
                "confirm_mixed_channel_risk": True,
            },
            admin_token,
            timeout=45,
        )
        result["account_create_status"] = status
        account_data = unwrap(payload)
        account_id = find_value(account_data, {"id"})
        if status not in (200, 201) or account_id is None:
            result["stage"] = "account_create"
            result["error"] = safe_error(payload, secrets)
            return 1
        try:
            account_id = int(account_id)
        except Exception:
            pass
        result["account_id_present"] = True

        status, payload = request(
            BASE + "/keys",
            "POST",
            {"name": name, "expires_in_days": 1},
            admin_token,
        )
        result["api_key_create_status"] = status
        key_data = unwrap(payload)
        key_id = find_value(key_data, {"id", "key_id"})
        gateway_key = find_value(key_data, {"key", "api_key"})
        if status not in (200, 201) or not gateway_key:
            result["stage"] = "api_key_create"
            result["error"] = safe_error(payload, secrets)
            return 1
        try:
            key_id = int(key_id)
        except Exception:
            pass

        status, payload = request(
            WEB_BASE + "/v1/chat/completions",
            "POST",
            {
                "model": "auto",
                "stream": False,
                "messages": [{"role": "user", "content": "请只回复 OK"}],
            },
            gateway_key,
            timeout=180,
        )
        result["chat_status"] = status
        chat_data = unwrap(payload)
        if status == 200 and isinstance(chat_data, dict):
            choices = chat_data.get("choices")
            if isinstance(choices, list) and choices:
                message = choices[0].get("message") if isinstance(choices[0], dict) else {}
                result["assistant_text"] = content_text((message or {}).get("content"))
            result["response_id_present"] = bool(chat_data.get("id"))
            result["ok"] = True
        else:
            result["stage"] = "chat_completions"
            result["error"] = safe_error(payload, secrets)
        return 0 if result["ok"] else 1
    except Exception as exc:
        result["stage"] = result.get("stage", "runtime")
        result["error"] = safe_error({"error_type": type(exc).__name__, "error": str(exc)}, secrets)
        return 1
    finally:
        cleanup = {}
        if key_id is not None and admin_token:
            status, payload = request(BASE + "/keys/" + str(key_id), "DELETE", bearer=admin_token)
            cleanup["api_key_status"] = status
            if status not in (200, 204):
                cleanup["api_key_error"] = safe_error(payload, secrets)
        if account_id is not None and admin_token:
            status, payload = request(BASE + "/admin/accounts/" + str(account_id), "DELETE", bearer=admin_token)
            cleanup["account_status"] = status
            if status not in (200, 204):
                cleanup["account_error"] = safe_error(payload, secrets)
        result["cleanup"] = cleanup
        print(json.dumps(result, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    raise SystemExit(main())
'''


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--remote-root", default=DEFAULT_REMOTE_ROOT)
    parser.add_argument("--expected-fingerprint", default=DEFAULT_FINGERPRINT)
    parser.add_argument(
        "--stdin-secrets",
        action="store_true",
        help="read SSH password and access token as two lines from stdin",
    )
    return parser.parse_args()


def read_secrets(use_stdin: bool) -> tuple[str, str]:
    if use_stdin:
        ssh_password = sys.stdin.readline().rstrip("\r\n")
        access_token = sys.stdin.readline().rstrip("\r\n")
        return ssh_password, access_token
    ssh_password = os.environ.get("SUB2API_SSH_PASSWORD", "")
    access_token = os.environ.get("CHATGPT_ACCESS_TOKEN", "")
    if not ssh_password:
        import getpass

        ssh_password = getpass.getpass("SSH password: ")
    if not access_token:
        import getpass

        access_token = getpass.getpass("ChatGPT access token: ")
    return ssh_password, access_token


def fingerprint(key: paramiko.PKey) -> str:
    digest = hashlib.sha256(key.asbytes()).digest()
    return "SHA256:" + base64.b64encode(digest).decode("ascii").rstrip("=")


def run(args: argparse.Namespace, ssh_password: str, access_token: str) -> int:
    transport = paramiko.Transport((args.host, args.port))
    client = None
    try:
        transport.start_client(timeout=30)
        actual = fingerprint(transport.get_remote_server_key())
        if actual != args.expected_fingerprint:
            raise RuntimeError("SSH host fingerprint mismatch")
        transport.auth_password("root", ssh_password)
        client = paramiko.SSHClient()
        client._transport = transport

        payload = base64.b64encode(REMOTE_TEST_CODE.encode("utf-8")).decode("ascii")
        command = (
            "cd "
            + "'"
            + args.remote_root.replace("'", "'\\''")
            + "' && python3 -c \"import base64;exec(base64.b64decode('"
            + payload
            + "'))\""
        )
        stdin, stdout, stderr = client.exec_command(command, timeout=240)
        stdin.write((access_token + "\n").encode("utf-8"))
        stdin.flush()
        stdin.channel.shutdown_write()
        output = stdout.read().decode("utf-8", "replace")
        error = stderr.read().decode("utf-8", "replace")
        status = stdout.channel.recv_exit_status()
        if output.strip():
            print(output.strip())
        if error.strip():
            print(error.strip(), file=sys.stderr)
        return status
    finally:
        if client is not None:
            client.close()
        else:
            transport.close()


def main() -> int:
    args = parse_args()
    ssh_password, access_token = read_secrets(args.stdin_secrets)
    if not ssh_password or not access_token:
        print(json.dumps({"ok": False, "stage": "input", "error": "missing secret input"}))
        return 2
    try:
        return run(args, ssh_password, access_token)
    except Exception as exc:
        print(json.dumps({"ok": False, "stage": "ssh", "error": type(exc).__name__}))
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
