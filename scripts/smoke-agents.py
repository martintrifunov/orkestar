#!/usr/bin/env python3
"""Check installed CLIs in an isolated daemon without submitting model prompts."""
import argparse
import base64
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", default="./orkestar")
parser.add_argument("--directory", default=".")
parser.add_argument("--settle", type=float, default=14)
args = parser.parse_args()
binary = str(Path(args.binary).resolve())
with tempfile.TemporaryDirectory(prefix="orkestar-smoke-", dir="/tmp") as runtime:
    env = dict(os.environ, ORKESTAR_RUNTIME_DIR=runtime, TERM="xterm-256color")
    with open(Path(runtime) / "daemon.log", "wb") as log:
        daemon = subprocess.Popen([binary, "daemon", "serve"], env=env,
                                  stdin=subprocess.DEVNULL, stdout=log, stderr=log,
                                  start_new_session=True)

        def connect(method, params=None):
            stream = socket.socket(socket.AF_UNIX)
            stream.settimeout(15)
            stream.connect(runtime + "/orkestar.sock")
            stream.sendall((json.dumps(dict(version=1, id="smoke", method=method,
                                           params=params or {})) + "\n").encode())
            reader = stream.makefile("rb")
            response = json.loads(reader.readline())
            if "error" in response:
                reader.close()
                stream.close()
                raise RuntimeError(response["error"])
            return stream, reader, response["result"]

        def call(method, params=None):
            stream, reader, result = connect(method, params)
            reader.close()
            stream.close()
            return result

        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    call("system.ping")
                    break
                except OSError:
                    if time.monotonic() > deadline:
                        raise RuntimeError("isolated daemon failed to start")
                    time.sleep(0.1)
            workspace = call("workspace.create", {"directory": str(Path(args.directory).resolve())})
            launched = []
            for adapter in ("claude-code", "codex", "opencode"):
                try:
                    launched.append(call("agent.launch", dict(workspace_id=workspace["id"],
                        adapter=adapter, columns=110, rows=32)))
                except RuntimeError:
                    print(f"{adapter}: unavailable or launch failed", flush=True)
            time.sleep(args.settle)
            for agent in launched:
                params = dict(terminal_id=agent["terminal_id"], screen=True)
                stream, reader, attached = connect("terminal.attach", params)
                content = attached["screen"]["content"]
                trust = "Hooks need review" in content
                # Typing in a consent menu can select an action. Keep that screen intact.
                typed = False
                if agent["adapter"] != "codex" and "permission" not in content.lower():
                    typed = True
                    marker = "orkestar-unsent-smoke"
                    stream.sendall((json.dumps(dict(version=1, command="input",
                        data=base64.b64encode(marker.encode()).decode())) + "\n").encode())
                reader.close()
                stream.close()
                time.sleep(0.5)
                stream, reader, reattached = connect("terminal.attach", dict(params, observe=True))
                reader.close()
                stream.close()
                if typed and marker not in reattached["screen"]["content"]:
                    raise RuntimeError(f"{agent['adapter']}: unsent text did not render")
                if not reattached["screen"]["content"].strip():
                    raise RuntimeError(f"{agent['adapter']}: empty screen on reattach")
                state = next(a for a in call("system.snapshot")["agents"] if a["id"] == agent["id"])
                print(f"{agent['adapter']}: screen/reattach OK; state={state['state']}; "
                      f"hooks={state.get('signal_source') == 'hooks'}; "
                      f"native_id={bool(state.get('native_session_id'))}; hook_review={trust}; unsent_text={typed}", flush=True)
        finally:
            try:
                call("system.shutdown")
            except (OSError, RuntimeError, ValueError):
                pass
            try:
                daemon.wait(timeout=10)
            except subprocess.TimeoutExpired:
                daemon.terminate()
                daemon.wait(timeout=5)
