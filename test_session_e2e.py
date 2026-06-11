#!/usr/bin/env python3
"""
End-to-end session dedup test using agent-lsp in JSON mode.

Sends sequential tool calls through gcf-proxy --session --verbose,
exploring overlapping areas of a real codebase. Measures compounding
savings across calls.

Usage:
    python3 test_session_e2e.py [workspace_path]
    Default workspace: ~/code/gcf-go
"""
import json
import subprocess
import sys
import os
import time

WORKSPACE = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/code/gcf-proxy")
PROXY = "/tmp/gcf-proxy-session"

def jsonrpc(id, method, params):
    return json.dumps({"jsonrpc": "2.0", "id": id, "method": method, "params": params})

def notification(method, params=None):
    msg = {"jsonrpc": "2.0", "method": method}
    if params:
        msg["params"] = params
    return json.dumps(msg)

def tool_call(id, name, args):
    return jsonrpc(id, "tools/call", {"name": name, "arguments": args})

# Tool calls that explore overlapping code areas in gcf-go
CALLS = [
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "main.go")]}),
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "rewriter.go")]}),
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "stats.go")]}),
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "jsonrpc.go")]}),
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "main.go"), os.path.join(WORKSPACE, "rewriter.go")]}),
]

def run():
    env = os.environ.copy()
    env["AGENT_LSP_OUTPUT_FORMAT"] = "json"
    env["GOWORK"] = "off"  # Prevent broken parent go.work from poisoning gopls

    proc = subprocess.Popen(
        [PROXY, "--session", "--verbose", "agent-lsp"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )

    def send(line):
        proc.stdin.write(line + "\n")
        proc.stdin.flush()

    def recv(timeout=30):
        """Read lines until we get a response with an id."""
        import select
        deadline = time.time() + timeout
        while time.time() < deadline:
            # Use select to avoid blocking forever
            import io
            line = proc.stdout.readline()
            if not line:
                return None
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
                # Skip notifications (no id)
                if "id" in msg:
                    return msg
                # Skip but print notifications
            except json.JSONDecodeError:
                continue
        return None

    # Initialize
    print("=== Initializing agent-lsp via proxy ===")
    send(jsonrpc(1, "initialize", {
        "protocolVersion": "2025-03-26",
        "capabilities": {},
        "clientInfo": {"name": "gcf-proxy-test", "version": "1.0"}
    }))
    resp = recv()
    if resp and "result" in resp:
        server_info = resp.get("result", {}).get("serverInfo", {})
        print(f"  Connected: {server_info.get('name', 'unknown')} {server_info.get('version', '')}")
    else:
        print(f"  Init failed: {resp}")
        proc.kill()
        return

    send(notification("notifications/initialized"))
    time.sleep(1)

    # Start LSP and add workspace
    print(f"  Starting LSP for: {WORKSPACE}")
    send(tool_call(2, "start_lsp", {"root_dir": WORKSPACE, "ready_timeout_seconds": 30}))
    resp = recv(timeout=60)
    if resp:
        content = resp.get("result", {}).get("content", [])
        text = content[0].get("text", "") if content else ""
        is_err = resp.get("result", {}).get("isError", False)
        print(f"  start_lsp: {'ERROR: ' + text[:100] if is_err else 'OK'}")
    else:
        print(f"  start_lsp: timeout")
        proc.kill()
        return
    print("  Waiting 15s for LSP to index...")
    time.sleep(15)  # Let LSP index the workspace

    # Send tool calls
    print(f"\n=== Sending {len(CALLS)} tool calls ===")
    print(f"  Workspace: {WORKSPACE}")
    print()

    results = []
    for i, (tool, args) in enumerate(CALLS):
        call_id = i + 10
        files = [f.split("/")[-1] for f in args.get("changed_files", [])]
        print(f"Call {i+1}: {tool}({', '.join(files)})")

        send(tool_call(call_id, tool, args))
        resp = recv(timeout=30)

        if not resp or "result" not in resp:
            print(f"  No result (resp={'none' if not resp else json.dumps(resp)[:100]})")
            results.append((0, 0, False))
            continue

        # Extract text content
        content = resp.get("result", {}).get("content", [])
        text = ""
        for block in content:
            if isinstance(block, dict) and block.get("type") == "text":
                text += block.get("text", "")

        size = len(text)
        is_gcf = text.startswith("GCF ")
        bare_refs = text.count("previously transmitted")

        print(f"  {size:>6} bytes | GCF={is_gcf} | bare_refs={bare_refs}")
        results.append((size, bare_refs, is_gcf))

        time.sleep(0.5)

    # Shutdown
    proc.stdin.close()
    try:
        stderr = proc.stderr.read()
        proc.wait(timeout=10)
    except:
        proc.kill()
        stderr = proc.stderr.read()

    # Print results
    print(f"\n{'='*60}")
    print(f"{'Call':<8} {'Bytes':>8} {'Bare refs':>10} {'vs Call 1':>12} {'GCF':>5}")
    print(f"{'='*60}")
    baseline = results[0][0] if results else 0
    for i, (size, bare, is_gcf) in enumerate(results):
        if i == 0:
            savings = "baseline"
        elif baseline > 0:
            savings = f"{100*(1-size/baseline):+.0f}%"
        else:
            savings = "n/a"
        print(f"  {i+1:<6} {size:>8} {bare:>10} {savings:>12} {'yes' if is_gcf else 'no':>5}")

    print(f"\n=== Proxy verbose output ===")
    for line in stderr.strip().split("\n"):
        if "gcf-proxy:" in line or "session" in line.lower() or "---" in line or "Tool calls" in line or "saved" in line or "Bytes" in line or "tokens" in line:
            print(f"  {line}")

if __name__ == "__main__":
    run()
