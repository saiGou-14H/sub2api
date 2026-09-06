#!/usr/bin/env python3
"""Exercise the Web Prompt Tool bridge through the local Codex CLI.

The SSH password, ChatGPT access token, and temporary gateway key are kept in
process memory. A disposable remote account and API key are removed in the
finally block, even when the Codex CLI or the upstream request fails.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import uuid

import paramiko


DEFAULT_HOST = "66.92.18.39"
DEFAULT_PORT = 3643
DEFAULT_REMOTE_ROOT = "/root/sub2api-deploy-9999"
DEFAULT_FINGERPRINT = "SHA256:LEF7k+LxS5/z1NKdhayg+J+OlhFwo+6mtIl4EHqRvSI"
DEFAULT_BASE_URL = "http://66.92.18.39:9999/v1"
DEFAULT_WORKDIR = r"D:\WebGpt\sub2api"


REMOTE_SETUP_CODE = r'''
import json
import sys
import urllib.error
import urllib.request
import uuid


BASE = "http://127.0.0.1:9999/api/v1"


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


def request(url, method="GET", body=None, bearer=None, timeout=45):
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
                return response.status, json.loads(raw.decode("utf-8", "replace"))
            except Exception:
                return response.status, {"raw": raw.decode("utf-8", "replace")[:1000]}
    except urllib.error.HTTPError as exc:
        raw = exc.read(2000).decode("utf-8", "replace")
        try:
            payload = json.loads(raw)
        except Exception:
            payload = {"raw": raw[:1000]}
        return exc.code, payload
    except Exception as exc:
        return 0, {"error_type": type(exc).__name__, "error": str(exc)[:1000]}


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


def main():
    token = sys.stdin.readline().strip()
    if not token:
        print(json.dumps({"ok": False, "stage": "input", "error": "access token is empty"}))
        return 2
    env = parse_env("/root/sub2api-deploy-9999/.env")
    status, login = request(
        BASE + "/auth/login",
        "POST",
        {"email": env.get("ADMIN_EMAIL", ""), "password": env.get("ADMIN_PASSWORD", "")},
    )
    login_data = unwrap(login)
    admin_token = str(login_data.get("access_token") or "") if isinstance(login_data, dict) else ""
    if status != 200 or not admin_token:
        print(json.dumps({"ok": False, "stage": "admin_login", "status": status}))
        return 1

    settings_status, settings_payload = request(BASE + "/admin/settings", bearer=admin_token)
    settings_data = unwrap(settings_payload)
    prompt_tools_enabled = (
        bool(settings_data.get("enable_openai_web_prompt_tools"))
        if settings_status == 200 and isinstance(settings_data, dict)
        else None
    )

    name = "codex-cli-web-tools-" + uuid.uuid4().hex[:10]
    group_id = None
    account_id = None
    key_id = None
    gateway_key = ""
    try:
        status, payload = request(
            BASE + "/admin/groups",
            "POST",
            {
                "name": name,
                "platform": "openai",
                "rate_multiplier": 1,
                "allow_live": True,
                "require_oauth_only": False,
                "require_privacy_set": False,
            },
            admin_token,
        )
        group_data = unwrap(payload)
        group_id = find_value(group_data, {"id"})
        if status not in (200, 201) or group_id is None:
            print(json.dumps({"ok": False, "stage": "group_create", "status": status}))
            return 1
        group_id = int(group_id)

        status, payload = request(
            BASE + "/admin/accounts",
            "POST",
            {
                "name": name,
                "platform": "openai",
                "type": "setup-token",
                "credentials": {"access_token": token},
                "extra": {"openai_transport": "web"},
                "group_ids": [group_id],
                "concurrency": 1,
                "priority": 0,
                "confirm_mixed_channel_risk": True,
            },
            admin_token,
        )
        account_data = unwrap(payload)
        account_id = find_value(account_data, {"id"})
        if status not in (200, 201) or account_id is None:
            print(json.dumps({"ok": False, "stage": "account_create", "status": status}))
            return 1
        account_id = int(account_id)

        status, payload = request(
            BASE + "/keys",
            "POST",
            {"name": name, "group_id": group_id, "expires_in_days": 1},
            admin_token,
        )
        key_data = unwrap(payload)
        key_id = find_value(key_data, {"id", "key_id"})
        gateway_key = str(find_value(key_data, {"key", "api_key"}) or "")
        if status not in (200, 201) or not gateway_key:
            print(json.dumps({"ok": False, "stage": "api_key_create", "status": status}))
            return 1
        print(json.dumps({
            "ok": True,
            "group_id": group_id,
            "account_id": account_id,
            "key_id": int(key_id),
            "gateway_key": gateway_key,
            "admin_token": admin_token,
            "prompt_tools_enabled": prompt_tools_enabled,
        }))
        return 0
    except Exception as exc:
        print(json.dumps({"ok": False, "stage": "runtime", "error_type": type(exc).__name__}))
        return 1
    finally:
        # The local harness performs cleanup after the CLI run. If setup fails
        # before the handoff, remove any partially-created account here.
        if gateway_key == "" and account_id is not None:
            request(BASE + "/admin/accounts/" + str(account_id), "DELETE", bearer=admin_token)
        if gateway_key == "" and group_id is not None:
            request(BASE + "/admin/groups/" + str(group_id), "DELETE", bearer=admin_token)


if __name__ == "__main__":
    raise SystemExit(main())
'''


REMOTE_CLEANUP_CODE = r'''
import json
import sys
import urllib.error
import urllib.request


BASE = "http://127.0.0.1:9999/api/v1"


def request(url, method, bearer):
    req = urllib.request.Request(
        url,
        headers={"Accept": "application/json", "Authorization": "Bearer " + bearer},
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            response.read()
            return response.status
    except urllib.error.HTTPError as exc:
        return exc.code
    except Exception:
        return 0


admin_token = sys.stdin.readline().strip()
key_id = sys.stdin.readline().strip()
account_id = sys.stdin.readline().strip()
group_id = sys.stdin.readline().strip()
result = {}
if admin_token and key_id:
    result["api_key_status"] = request(BASE + "/keys/" + key_id, "DELETE", admin_token)
if admin_token and account_id:
    result["account_status"] = request(BASE + "/admin/accounts/" + account_id, "DELETE", admin_token)
if admin_token and group_id:
    result["group_status"] = request(BASE + "/admin/groups/" + group_id, "DELETE", admin_token)
print(json.dumps(result, sort_keys=True))
'''


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--remote-root", default=DEFAULT_REMOTE_ROOT)
    parser.add_argument("--expected-fingerprint", default=DEFAULT_FINGERPRINT)
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default="auto")
    parser.add_argument("--codex", default="codex")
    parser.add_argument("--workdir", default=DEFAULT_WORKDIR)
    parser.add_argument("--target", default=r"D:\test.py")
    parser.add_argument("--stdin-secrets", action="store_true")
    return parser.parse_args()


def read_secrets(use_stdin: bool) -> tuple[str, str]:
    if use_stdin:
        return sys.stdin.readline().rstrip("\r\n"), sys.stdin.readline().rstrip("\r\n")
    import getpass

    return getpass.getpass("SSH password: "), getpass.getpass("ChatGPT access token: ")


def fingerprint(key: paramiko.PKey) -> str:
    digest = hashlib.sha256(key.asbytes()).digest()
    return "SHA256:" + base64.b64encode(digest).decode("ascii").rstrip("=")


def run_remote(client: paramiko.SSHClient, code: str, lines: list[str], timeout: int = 120) -> str:
    payload = base64.b64encode(code.encode("utf-8")).decode("ascii")
    command = "python3 -c \"import base64;exec(base64.b64decode('" + payload + "'))\""
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
    for line in lines:
        stdin.write(line + "\n")
    stdin.flush()
    stdin.channel.shutdown_write()
    output = stdout.read().decode("utf-8", "replace").strip()
    error = stderr.read().decode("utf-8", "replace").strip()
    status = stdout.channel.recv_exit_status()
    if status != 0 and error:
        raise RuntimeError("remote helper failed: " + error[:1000])
    return output


def summarize_cli(stdout: str, stderr: str, target: str) -> dict[str, object]:
    event_types: dict[str, int] = {}
    tool_events = 0
    text_fragments: list[str] = []
    for raw in stdout.splitlines():
        line = raw.strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except Exception:
            continue
        if not isinstance(value, dict):
            continue
        event_type = value.get("type")
        if isinstance(event_type, str):
            event_types[event_type] = event_types.get(event_type, 0) + 1
            if "tool" in event_type or "command" in event_type:
                tool_events += 1
        if event_type in {"item.completed", "response.output_text.done"}:
            text = json.dumps(value, ensure_ascii=False)
            if text:
                text_fragments.append(text[:600])
    path = Path(target)
    return {
        "cli_exit_code": None,
        "tool_event_count": tool_events,
        "event_types": event_types,
        "target_exists": path.exists(),
        "target_size": path.stat().st_size if path.exists() else 0,
        "target_preview": path.read_text(encoding="utf-8", errors="replace")[:200] if path.exists() else "",
        "stderr_tail": re.sub(r"Bearer\\s+\\S+", "Bearer <redacted>", stderr[-1000:]),
        "event_samples": text_fragments[-3:],
    }


def main() -> int:
    args = parse_args()
    ssh_password, access_token = read_secrets(args.stdin_secrets)
    if not ssh_password or not access_token:
        print(json.dumps({"ok": False, "stage": "input", "error": "missing secret input"}))
        return 2

    transport = paramiko.Transport((args.host, args.port))
    client = None
    setup = None
    try:
        transport.start_client(timeout=30)
        actual = fingerprint(transport.get_remote_server_key())
        if actual != args.expected_fingerprint:
            raise RuntimeError("SSH host fingerprint mismatch")
        transport.auth_password("root", ssh_password)
        client = paramiko.SSHClient()
        client._transport = transport

        setup_raw = run_remote(client, REMOTE_SETUP_CODE, [access_token], timeout=120)
        setup = json.loads(setup_raw.splitlines()[-1])
        if not setup.get("ok"):
            print(json.dumps(setup, sort_keys=True))
            return 1

        with tempfile.TemporaryDirectory(prefix="codex-cli-web-tools-") as codex_home:
            env = os.environ.copy()
            # Current Codex CLI automation reads CODEX_API_KEY. Keep the
            # OpenAI name as a compatibility fallback for older clients.
            env["CODEX_API_KEY"] = str(setup["gateway_key"])
            env["OPENAI_API_KEY"] = str(setup["gateway_key"])
            env["CODEX_HOME"] = codex_home
            # Custom Codex providers read credentials from CODEX_HOME/auth.json
            # when user config is ignored. Keep this file inside the disposable
            # temp home so the generated gateway key never enters the repo.
            Path(codex_home, "auth.json").write_text(
                json.dumps({"OPENAI_API_KEY": str(setup["gateway_key"])}) + "\n",
                encoding="utf-8",
            )
            command = [
                args.codex,
                "exec",
                "--ephemeral",
                "--json",
                "--ignore-user-config",
                "--skip-git-repo-check",
                "--sandbox",
                "danger-full-access",
                "--add-dir",
                "D:" + "\\",
                "-C",
                args.workdir,
                "-m",
                args.model,
                "-c",
                'model_provider="OpenAI"',
                "-c",
                'disable_response_storage=true',
                "-c",
                'model_providers.OpenAI.name="OpenAI"',
                "-c",
                'model_providers.OpenAI.base_url="' + args.base_url + '"',
                "-c",
                'model_providers.OpenAI.wire_api="responses"',
                "-c",
                'model_providers.OpenAI.requires_openai_auth=true',
                "-c",
                'features.goals=true',
                "-c",
                'features.plugins=false',
                (
                    "Use the local Windows PowerShell tool to create exactly "
                    + args.target
                    + " containing: print(\"hello from codex cli\") . Verify the file exists "
                    "and report only the result. Do not claim that the D drive is inaccessible."
                ),
            ]
            completed = subprocess.run(
                command,
                cwd=args.workdir,
                env=env,
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=240,
            )
            summary = summarize_cli(completed.stdout, completed.stderr, args.target)
            summary["cli_exit_code"] = completed.returncode
            summary["prompt_tools_enabled"] = setup.get("prompt_tools_enabled")
            summary["ok"] = completed.returncode == 0 and bool(summary["target_exists"])
            print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
            result_code = 0 if summary["ok"] else 1

        cleanup_raw = run_remote(
            client,
            REMOTE_CLEANUP_CODE,
            [str(setup["admin_token"]), str(setup["key_id"]), str(setup["account_id"]), str(setup["group_id"])],
            timeout=90,
        )
        cleanup = json.loads(cleanup_raw.splitlines()[-1])
        if (
            cleanup.get("api_key_status") not in (200, 204)
            or cleanup.get("account_status") not in (200, 204)
            or cleanup.get("group_status") not in (200, 204)
        ):
            print(json.dumps({"ok": False, "stage": "cleanup", "cleanup": cleanup}, sort_keys=True))
            return 1
        return result_code
    except subprocess.TimeoutExpired:
        print(json.dumps({"ok": False, "stage": "codex_cli", "error": "timeout"}))
        return 1
    except Exception as exc:
        print(json.dumps({"ok": False, "stage": "runtime", "error_type": type(exc).__name__, "error": str(exc)[:1000]}))
        return 1
    finally:
        if setup and setup.get("ok") and client is not None:
            # Best-effort cleanup when the CLI process or JSON parsing fails.
            try:
                run_remote(
                    client,
                    REMOTE_CLEANUP_CODE,
                    [str(setup["admin_token"]), str(setup["key_id"]), str(setup["account_id"]), str(setup["group_id"])],
                    timeout=90,
                )
            except Exception:
                pass
        if client is not None:
            client.close()
        else:
            transport.close()


if __name__ == "__main__":
    raise SystemExit(main())
