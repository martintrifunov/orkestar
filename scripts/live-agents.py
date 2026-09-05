#!/usr/bin/env python3
"""Exercise installed, authenticated CLIs through an isolated Orkestar daemon.

Unlike smoke-agents.py this submits real model prompts, so it costs tokens and
needs the CLI to be logged in. It drives one adapter at a time through: launch,
a plain turn, a permission request denied then allowed, an interrupt, exit and
explicit native resume. It prints booleans and timings only; screen content and
credentials are never recorded. Runs in a fresh temporary workspace and shuts
down only its own daemon.
"""
import argparse
import base64
import json
import os
import socket
import subprocess
import tempfile
import time
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", default="./orkestar")
parser.add_argument("--adapters", default="claude-code,codex,opencode")
parser.add_argument("--turn-timeout", type=float, default=120)
parser.add_argument("--start-timeout", type=float, default=45)
args = parser.parse_args()
binary = str(Path(args.binary).resolve())

PING = "Reply with exactly the single word PONG and nothing else. Do not use any tools."
PERMISSION = ("Use your shell/bash tool to run exactly this command and nothing else: "
              "echo orkestar-live-permission. After it runs, or if it is refused, reply with only the word DONE.")
LONG = "Without using any tools, count slowly from 1 to 400 writing one number per line."
RECALL = "Earlier in this same conversation I asked you to reply with one specific word. Reply with only that word."


class Daemon:
    def __init__(self, runtime):
        self.runtime = runtime
        env = dict(os.environ, ORKESTAR_RUNTIME_DIR=runtime, TERM="xterm-256color")
        self.log = open(Path(runtime) / "daemon.log", "wb")
        self.process = subprocess.Popen([binary, "daemon", "serve"], env=env, stdin=subprocess.DEVNULL,
                                        stdout=self.log, stderr=self.log, start_new_session=True)
        deadline = time.monotonic() + 10
        while True:
            try:
                self.call("system.ping")
                return
            except OSError:
                if time.monotonic() > deadline:
                    raise RuntimeError("isolated daemon failed to start")
                time.sleep(0.1)

    def connect(self, method, params=None):
        stream = socket.socket(socket.AF_UNIX)
        stream.settimeout(30)
        stream.connect(self.runtime + "/orkestar.sock")
        stream.sendall((json.dumps(dict(version=1, id="live", method=method, params=params or {})) + "\n").encode())
        reader = stream.makefile("rb")
        response = json.loads(reader.readline())
        if "error" in response:
            reader.close()
            stream.close()
            raise RuntimeError(response["error"])
        return stream, reader, response["result"]

    def call(self, method, params=None):
        stream, reader, result = self.connect(method, params)
        reader.close()
        stream.close()
        return result

    def screen(self, terminal_id):
        stream, reader, attached = self.connect("terminal.attach", dict(terminal_id=terminal_id, screen=True, observe=True))
        reader.close()
        stream.close()
        return attached["screen"]["content"]

    def type_raw(self, terminal_id, data):
        """Send bytes as the controller; used only to answer a CLI's own startup dialog."""
        stream, reader, attached = self.connect("terminal.attach", dict(terminal_id=terminal_id, screen=True))
        ok = attached.get("controller", False)
        if ok:
            stream.sendall((json.dumps(dict(version=1, command="input", data=base64.b64encode(data).decode())) + "\n").encode())
            time.sleep(0.3)
        reader.close()
        stream.close()
        return ok

    def agent(self, agent_id):
        return next(a for a in self.call("system.snapshot")["agents"] if a["id"] == agent_id)

    def permissions(self, agent_id):
        return [p for p in self.call("system.snapshot").get("permissions") or [] if p["agent_id"] == agent_id]

    def wait(self, predicate, timeout, interval=0.5):
        start = time.monotonic()
        while time.monotonic() - start < timeout:
            value = predicate()
            if value:
                return value, round(time.monotonic() - start, 1)
            time.sleep(interval)
        return None, round(time.monotonic() - start, 1)

    def shutdown(self):
        try:
            self.call("system.shutdown")
        except (OSError, RuntimeError, ValueError):
            pass
        try:
            self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            self.process.wait(timeout=5)
        self.log.close()


def run_adapter(name, daemon, workspace):
    report = {"adapter": name}
    launched = daemon.call("agent.launch", dict(workspace_id=workspace["id"], adapter=name, columns=120, rows=36))
    agent_id, terminal_id = launched["id"], launched["terminal_id"]

    def settled():
        a = daemon.agent(agent_id)
        content = daemon.screen(terminal_id)
        if a["state"] in ("stopped", "crashed"):
            return "exited"
        if "Hooks need review" in content:
            return "hook-review"
        lowered = content.lower()
        if "trust" in lowered and ("folder" in lowered or "directory" in lowered or "workspace" in lowered):
            return "trust-dialog"
        if a.get("signal_source") == "hooks" or a["state"] == "ready":
            return "ready"
        return None

    outcome, took = daemon.wait(settled, args.start_timeout)
    report["startup"] = outcome or "no-signal"
    report["startup_seconds"] = took
    if outcome == "trust-dialog":
        # The CLI asks whether to trust the temporary workspace this script created.
        report["accepted_own_temp_dir_trust"] = daemon.type_raw(terminal_id, b"\r")
        outcome, took = daemon.wait(lambda: settled() if settled() != "trust-dialog" else None, args.start_timeout)
        report["startup_after_trust"] = outcome or "no-signal"
    if outcome == "hook-review":
        report["note"] = "native hook trust review shown; not answered by this script"
        return report
    if outcome in ("exited", "no-signal", None):
        report["note"] = "agent did not become ready"
        return report
    a = daemon.agent(agent_id)
    report["hooks"] = a.get("signal_source") == "hooks"
    report["native_id_at_start"] = bool(a.get("native_session_id"))

    def turn(text, until, timeout=args.turn_timeout):
        daemon.call("agent.prompt", dict(agent_id=agent_id, text=text))
        working, _ = daemon.wait(lambda: daemon.agent(agent_id)["state"] in ("working", "waiting_permission"), 20)
        done, took = daemon.wait(until, timeout)
        return bool(working), bool(done), took

    working, done, took = turn(PING, lambda: daemon.agent(agent_id)["state"] == "ready" and "PONG" in daemon.screen(terminal_id))
    report["turn"] = dict(working_observed=working, completed=done, seconds=took)
    if not done:
        report["note"] = "plain turn did not complete; later steps skipped"
        return report
    report["native_id_after_turn"] = bool(daemon.agent(agent_id).get("native_session_id"))

    daemon.call("agent.prompt", dict(agent_id=agent_id, text=PERMISSION))
    pending, took = daemon.wait(lambda: daemon.permissions(agent_id), 60)
    perm = dict(requested=bool(pending), seconds=took)
    if pending:
        perm["state_while_pending"] = daemon.agent(agent_id)["state"]
        daemon.call("permission.resolve", dict(permission_id=pending[0]["id"], decision="deny"))
        done, took = daemon.wait(lambda: not daemon.permissions(agent_id) and daemon.agent(agent_id)["state"] == "ready", args.turn_timeout)
        perm["deny_released_turn"] = bool(done)
        perm["deny_seconds"] = took
        daemon.call("agent.prompt", dict(agent_id=agent_id, text=PERMISSION))
        pending, _ = daemon.wait(lambda: daemon.permissions(agent_id), 60)
        perm["requested_again"] = bool(pending)
        if pending:
            daemon.call("permission.resolve", dict(permission_id=pending[0]["id"], decision="allow"))
            done, took = daemon.wait(lambda: daemon.agent(agent_id)["state"] == "ready" and "orkestar-live-permission" in daemon.screen(terminal_id), args.turn_timeout)
            perm["allow_ran_command"] = bool(done)
            perm["allow_seconds"] = took
    else:
        daemon.wait(lambda: daemon.agent(agent_id)["state"] == "ready", args.turn_timeout)
        perm["note"] = "no permission request surfaced; the CLI may auto-allow this command"
    report["permission"] = perm

    daemon.call("agent.prompt", dict(agent_id=agent_id, text=LONG))
    working, _ = daemon.wait(lambda: daemon.agent(agent_id)["state"] == "working", 30)
    time.sleep(3)
    daemon.call("agent.interrupt", dict(agent_id=agent_id))
    done, took = daemon.wait(lambda: daemon.agent(agent_id)["state"] == "ready", 45)
    report["interrupt"] = dict(working_before=bool(working), ready_after=bool(done), seconds=took)

    daemon.call("agent.prompt", dict(agent_id=agent_id, text="/exit"))
    done, took = daemon.wait(lambda: daemon.agent(agent_id)["state"] in ("stopped", "crashed"), 45)
    if not done:
        daemon.type_raw(terminal_id, b"/exit\r")
        done, took = daemon.wait(lambda: daemon.agent(agent_id)["state"] in ("stopped", "crashed"), 45)
        report["exit_needed_raw_cr"] = True
    report["exit"] = dict(stopped=bool(done), state=daemon.agent(agent_id)["state"], seconds=took)
    if not done or not daemon.agent(agent_id).get("native_session_id"):
        report["resume"] = "skipped: no stopped agent with a native session id"
        return report

    resumed = daemon.call("agent.resume", dict(agent_id=agent_id))
    rid, rterm = resumed["id"], resumed["terminal_id"]
    ready, took = daemon.wait(lambda: daemon.agent(rid)["state"] == "ready" or daemon.agent(rid).get("signal_source") == "hooks", args.start_timeout)
    res = dict(new_agent=rid != agent_id, same_native_id=resumed.get("native_session_id") == daemon.agent(agent_id).get("native_session_id"), ready=bool(ready), start_seconds=took)
    if ready:
        daemon.call("agent.prompt", dict(agent_id=rid, text=RECALL))
        done, took = daemon.wait(lambda: daemon.agent(rid)["state"] == "ready" and "PONG" in daemon.screen(rterm), args.turn_timeout)
        res["recalled_earlier_turn"] = bool(done)
        res["recall_seconds"] = took
        daemon.call("agent.prompt", dict(agent_id=rid, text="/exit"))
        daemon.wait(lambda: daemon.agent(rid)["state"] in ("stopped", "crashed"), 30)
    report["resume"] = res
    return report


for name in args.adapters.split(","):
    name = name.strip()
    with tempfile.TemporaryDirectory(prefix="orkestar-live-", dir="/tmp") as runtime, tempfile.TemporaryDirectory(prefix="orkestar-live-ws-") as ws:
        subprocess.run(["git", "-C", ws, "init", "-q"], check=True)
        daemon = Daemon(runtime)
        try:
            workspace = daemon.call("workspace.create", {"directory": ws})
            try:
                report = run_adapter(name, daemon, workspace)
            except RuntimeError as error:
                report = {"adapter": name, "error": str(error)}
        finally:
            daemon.shutdown()
        print(json.dumps(report, indent=2), flush=True)
