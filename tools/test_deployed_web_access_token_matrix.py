#!/usr/bin/env python3
"""Run a disposable, direct-to-host Web access-token regression matrix.

The SSH password and ChatGPT access token are read ephemerally.  The remote
probe creates a private temporary OpenAI group/account/key, exercises both
public OpenAI endpoints, and removes all temporary objects in ``finally``.
No credential value is written to a file or included in the result.
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
import base64
import json
import os
import re
import sys
import urllib.error
import urllib.request
import uuid


BASE = "http://127.0.0.1:9999/api/v1"
PUBLIC = "http://127.0.0.1:9999"
DIRECT_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def parse_env(path):
    values = {}
    with open(path, "r", encoding="utf-8") as stream:
        for raw in stream:
            line = raw.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            values[key.strip()] = value
    return values


def safe_error(value, secrets):
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    for secret in secrets:
        if secret:
            text = text.replace(secret, "<redacted>")
    text = re.sub(r"(?i)(bearer\s+)[^\s\"}]+", r"\1<redacted>", text)
    return text[:1600]


def request(url, method="GET", body=None, bearer=None, timeout=35, stream=False):
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        response = DIRECT_OPENER.open(req, timeout=timeout)
        if stream:
            return response.status, None, response
        raw = response.read(4 * 1024 * 1024 + 1)
        response.close()
        return response.status, decode_json(raw), None
    except urllib.error.HTTPError as exc:
        if stream:
            raw = exc.read(16000).decode("utf-8", "replace")
        else:
            raw = exc.read(16000).decode("utf-8", "replace")
        try:
            parsed = json.loads(raw)
        except Exception:
            parsed = {"raw": raw[:1600]}
        return exc.code, parsed, None
    except Exception as exc:
        return 0, {"error_type": type(exc).__name__, "error": str(exc)[:1200]}, None


def decode_json(raw):
    try:
        return json.loads(raw.decode("utf-8", "replace"))
    except Exception:
        return {"raw": raw.decode("utf-8", "replace")[:1600]}


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
                elif isinstance(item.get("content"), (str, list, dict)):
                    parts.append(content_text(item["content"]))
        return "".join(parts)
    if isinstance(value, dict):
        return content_text(value.get("text") or value.get("value") or value.get("content") or "")
    return ""


def response_text(payload):
    payload = unwrap(payload)
    if not isinstance(payload, dict):
        return ""
    choices = payload.get("choices")
    if isinstance(choices, list) and choices and isinstance(choices[0], dict):
        message = choices[0].get("message")
        if isinstance(message, dict):
            return content_text(message.get("content"))[:240]
    output = payload.get("output")
    if isinstance(output, list):
        return content_text(output)[:240]
    return content_text(payload.get("text"))[:240]


def response_id(payload):
    payload = unwrap(payload)
    return bool(isinstance(payload, dict) and (payload.get("id") or payload.get("response_id")))


def read_sse(response, limit=2 * 1024 * 1024):
    events = []
    data_lines = []
    total = 0
    saw_done = False
    saw_completed = False
    try:
        while total < limit:
            raw = response.readline()
            if not raw:
                break
            total += len(raw)
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line.startswith("event:"):
                event = line[6:].strip()
                if event:
                    events.append(event)
                    if event in ("response.completed", "response.done"):
                        saw_completed = True
            elif line.startswith("data:"):
                value = line[5:].strip()
                data_lines.append(value)
                if value == "[DONE]":
                    saw_done = True
                if "response.completed" in value:
                    saw_completed = True
            elif line == "":
                data_lines = []
    finally:
        response.close()
    return {
        "events": len(events),
        "event_names": events[-12:],
        "done": saw_done,
        "completed": saw_completed,
        "bytes": total,
    }


def data_uri(mime, raw):
    return "data:" + mime + ";base64," + base64.b64encode(raw).decode("ascii")


def run_request(result, label, path, body, key, secrets, timeout=180, stream=False):
    status, payload, response = request(PUBLIC + path, "POST", body, key, timeout, stream=stream)
    item = {"status": status, "stream": stream}
    if stream:
        if response is not None and status == 200:
            item.update(read_sse(response))
            item["ok"] = bool(item.get("completed") or item.get("done"))
        else:
            item["ok"] = False
            item["error"] = safe_error(payload, secrets)
    else:
        item["ok"] = status == 200 and isinstance(payload, dict)
        if item["ok"]:
            item["assistant_text"] = response_text(payload)
            item["response_id_present"] = response_id(payload)
        else:
            item["error"] = safe_error(payload, secrets)
    result[label] = item
    return item["ok"]


def main():
    token = sys.stdin.readline().strip()
    if not token:
        print(json.dumps({"ok": False, "stage": "input", "error": "access token is empty"}))
        return 2

    cfg = parse_env("/root/sub2api-deploy-9999/.env")
    admin_email = cfg.get("ADMIN_EMAIL", "")
    admin_password = cfg.get("ADMIN_PASSWORD", "")
    secrets = [token, admin_password]
    result = {"ok": False, "transport": "web", "proxy_configured": False, "endpoint": PUBLIC}
    admin_token = ""
    group_id = None
    account_id = None
    key_id = None
    gateway_key = ""
    name = "web-matrix-" + uuid.uuid4().hex[:10]

    try:
        status, payload, _ = request(BASE + "/auth/login", "POST", {"email": admin_email, "password": admin_password}, timeout=45)
        result["login_status"] = status
        login = unwrap(payload)
        if status != 200 or not isinstance(login, dict):
            result["stage"] = "admin_login"
            result["error"] = safe_error(payload, secrets)
            return 1
        admin_token = str(login.get("access_token") or "")
        if not admin_token:
            result["stage"] = "admin_login"
            result["error"] = "login response did not contain an access token"
            return 1

        status, payload, _ = request(
            BASE + "/admin/groups",
            "POST",
            {
                "name": name,
                "description": "temporary direct Web access-token regression group",
                "platform": "openai",
                "rate_multiplier": 1.0,
                "is_exclusive": False,
                "subscription_type": "standard",
                "allow_live": False,
                "require_oauth_only": False,
                "require_privacy_set": False,
            },
            admin_token,
            timeout=45,
        )
        result["group_create_status"] = status
        group = unwrap(payload)
        group_id = find_value(group, {"id", "group_id"})
        if status not in (200, 201) or group_id is None:
            result["stage"] = "group_create"
            result["error"] = safe_error(payload, secrets)
            return 1
        group_id = int(group_id)
        result["group_id_present"] = True

        status, payload, _ = request(
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
                "group_ids": [group_id],
                "confirm_mixed_channel_risk": True,
            },
            admin_token,
            timeout=60,
        )
        result["account_create_status"] = status
        account = unwrap(payload)
        account_id = find_value(account, {"id", "account_id"})
        if status not in (200, 201) or account_id is None:
            result["stage"] = "account_create"
            result["error"] = safe_error(payload, secrets)
            return 1
        account_id = int(account_id)
        result["account_id_present"] = True

        status, payload, _ = request(
            BASE + "/keys",
            "POST",
            {"name": name, "group_id": group_id, "expires_in_days": 1},
            admin_token,
            timeout=45,
        )
        result["api_key_create_status"] = status
        key = unwrap(payload)
        key_id = find_value(key, {"id", "key_id"})
        gateway_key = str(find_value(key, {"key", "api_key"}) or "")
        if status not in (200, 201) or not gateway_key:
            result["stage"] = "api_key_create"
            result["error"] = safe_error(payload, secrets)
            return 1
        key_id = int(key_id)

        result["tests"] = {}
        chat_text = {"model": "auto", "stream": False, "messages": [{"role": "user", "content": "Reply with OK"}]}
        chat_stream = {"model": "auto", "stream": True, "messages": [{"role": "user", "content": "Reply with OK"}]}
        responses_text = {"model": "auto", "stream": False, "input": "Reply with OK"}
        responses_stream = {"model": "auto", "stream": True, "input": "Reply with OK"}
        run_request(result["tests"], "chat_text", "/v1/chat/completions", chat_text, gateway_key, secrets)
        run_request(result["tests"], "chat_stream", "/v1/chat/completions", chat_stream, gateway_key, secrets, stream=True)
        run_request(result["tests"], "responses_text", "/v1/responses", responses_text, gateway_key, secrets)
        run_request(result["tests"], "responses_stream", "/v1/responses", responses_stream, gateway_key, secrets, stream=True)

        png = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0dIDAT\x08\xd7c\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82"
        fixtures = [
            ("png", "image/png", "sample.png", png),
            ("pdf", "application/pdf", "sample.pdf", b"%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n"),
            ("txt", "text/plain", "sample.txt", b"web attachment smoke test\n"),
            ("zip", "application/zip", "sample.zip", b"PK\x05\x06" + b"\x00" * 18),
        ]
        for label, mime, filename, raw in fixtures:
            uri = data_uri(mime, raw)
            chat_body = {
                "model": "auto",
                "stream": False,
                "messages": [{"role": "user", "content": [
                    {"type": "text", "text": "Identify this attachment briefly."},
                    ({"type": "image_url", "image_url": {"url": uri}} if mime.startswith("image/") else
                     {"type": "file", "file": {"filename": filename, "file_data": uri}}),
                ]}],
            }
            responses_body = {
                "model": "auto",
                "stream": False,
                "input": [{"role": "user", "content": [
                    {"type": "input_text", "text": "Identify this attachment briefly."},
                    ({"type": "input_image", "image_url": uri} if mime.startswith("image/") else
                     {"type": "input_file", "filename": filename, "file_data": uri}),
                ]}],
            }
            run_request(result["tests"], "chat_attachment_" + label, "/v1/chat/completions", chat_body, gateway_key, secrets)
            run_request(result["tests"], "responses_attachment_" + label, "/v1/responses", responses_body, gateway_key, secrets)

        result["ok"] = all(item.get("ok") for item in result["tests"].values())
        if not result["ok"]:
            result["stage"] = "matrix"
        return 0 if result["ok"] else 1
    except Exception as exc:
        result["stage"] = result.get("stage", "runtime")
        result["error"] = safe_error({"error_type": type(exc).__name__, "error": str(exc)}, secrets)
        return 1
    finally:
        cleanup = {}
        if key_id is not None and admin_token:
            status, payload, _ = request(BASE + "/keys/" + str(key_id), "DELETE", bearer=admin_token, timeout=45)
            cleanup["api_key_status"] = status
            if status not in (200, 204):
                cleanup["api_key_error"] = safe_error(payload, secrets)
        if account_id is not None and admin_token:
            status, payload, _ = request(BASE + "/admin/accounts/" + str(account_id), "DELETE", bearer=admin_token, timeout=45)
            cleanup["account_status"] = status
            if status not in (200, 204):
                cleanup["account_error"] = safe_error(payload, secrets)
        if group_id is not None and admin_token:
            status, payload, _ = request(BASE + "/admin/groups/" + str(group_id), "DELETE", bearer=admin_token, timeout=45)
            cleanup["group_status"] = status
            if status not in (200, 204):
                cleanup["group_error"] = safe_error(payload, secrets)
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
        return sys.stdin.readline().rstrip("\r\n"), sys.stdin.readline().rstrip("\r\n")
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
        if fingerprint(transport.get_remote_server_key()) != args.expected_fingerprint:
            raise RuntimeError("SSH host fingerprint mismatch")
        transport.auth_password("root", ssh_password)
        client = paramiko.SSHClient()
        client._transport = transport
        payload = base64.b64encode(REMOTE_TEST_CODE.encode("utf-8")).decode("ascii")
        root = args.remote_root.replace("'", "'\\''")
        command = "cd '" + root + "' && python3 -c \"import base64;exec(base64.b64decode('" + payload + "'))\""
        stdin, stdout, stderr = client.exec_command(command, timeout=3600)
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
