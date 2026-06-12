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

WORKSPACE = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/code/gcf-typescript")
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
    # Call 1: blast_radius on generic.ts (baseline)
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "src/generic.ts")]}),
    # Call 2: same file again (tests session dedup: should be bare refs)
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "src/generic.ts")]}),
    # Call 3: scalar.ts (some overlap with call 1)
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "src/scalar.ts")]}),
    # Call 4: generic.ts again (tests cache: identical upstream response to call 2)
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "src/generic.ts")]}),
    # Call 5: both files (mix of known and new)
    ("blast_radius", {"changed_files": [os.path.join(WORKSPACE, "src/generic.ts"), os.path.join(WORKSPACE, "src/scalar.ts")]}),
    # Call 6: list_symbols on a small file (tests min-size bypass if response < 100 bytes)
    ("list_symbols", {"file_path": os.path.join(WORKSPACE, "src/constants.ts")}),
]

def get_json_baselines():
    """Run agent-lsp in JSON mode to get baseline JSON sizes for each call."""
    env = os.environ.copy()
    env["AGENT_LSP_OUTPUT_FORMAT"] = "json"
    env["GOWORK"] = "off"

    proc = subprocess.Popen(
        ["agent-lsp"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, env=env,
    )

    def send(msg):
        proc.stdin.write(msg + "\n")
        proc.stdin.flush()

    def recv_id(tid, timeout=60):
        deadline = time.time() + timeout
        while time.time() < deadline:
            line = proc.stdout.readline().strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
                if msg.get("id") == tid:
                    return msg
            except json.JSONDecodeError:
                pass
        return None

    # Initialize
    send(json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
        "protocolVersion": "2025-03-26", "capabilities": {},
        "clientInfo": {"name": "json-baseline", "version": "1.0"}
    }}))
    recv_id(1)
    send(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}))
    time.sleep(1)

    start_args = {"root_dir": WORKSPACE, "ready_timeout_seconds": 30}
    if "typescript" in WORKSPACE:
        start_args["language_id"] = "typescript"
    send(json.dumps({"jsonrpc": "2.0", "id": 2, "method": "tools/call",
                      "params": {"name": "start_lsp", "arguments": start_args}}))
    recv_id(2, timeout=60)
    time.sleep(15)

    baselines = {}
    for i, (tool, args) in enumerate(CALLS):
        call_id = i + 10
        send(json.dumps({"jsonrpc": "2.0", "id": call_id, "method": "tools/call",
                          "params": {"name": tool, "arguments": args}}))
        resp = recv_id(call_id, timeout=30)
        if resp and "result" in resp:
            content = resp.get("result", {}).get("content", [])
            text = ""
            for block in content:
                if isinstance(block, dict) and block.get("type") == "text":
                    text += block.get("text", "")
            baselines[i] = len(text)
        else:
            baselines[i] = 0

    proc.stdin.close()
    try:
        proc.wait(timeout=5)
    except:
        proc.kill()

    return baselines

def run():
    # Phase 0: get JSON baselines
    print("=== Phase 0: JSON baselines (agent-lsp in JSON mode) ===")
    json_baselines = get_json_baselines()
    for i, size in json_baselines.items():
        if "changed_files" in CALLS[i][1]:
            label = ', '.join(f.split("/")[-1] for f in CALLS[i][1]["changed_files"])
        elif "file_path" in CALLS[i][1]:
            label = CALLS[i][1]["file_path"].split("/")[-1]
        else:
            label = "?"
        print(f"  {CALLS[i][0]}({label}): {size:,} bytes JSON")
    print()

    env = os.environ.copy()
    # Let agent-lsp produce GCF natively. The proxy's GCF-in path decodes
    # and re-encodes with session dedup (bare refs for known symbols).
    env.pop("AGENT_LSP_OUTPUT_FORMAT", None)
    env["GOWORK"] = "off"  # Prevent broken parent go.work from poisoning gopls

    proc = subprocess.Popen(
        [PROXY, "--session", "--min-size", "100", "--verbose", "agent-lsp"],
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
    start_args = {"root_dir": WORKSPACE, "ready_timeout_seconds": 30}
    # Auto-detect language from workspace
    if "typescript" in WORKSPACE:
        start_args["language_id"] = "typescript"
    send(tool_call(2, "start_lsp", start_args))
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
        if "changed_files" in args:
            label = ', '.join(f.split("/")[-1] for f in args["changed_files"])
        elif "file_path" in args:
            label = args["file_path"].split("/")[-1]
        else:
            label = str(args)[:40]
        print(f"Call {i+1}: {tool}({label})")

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

    # Print results with JSON comparison
    print(f"\n{'='*72}")
    print(f"{'Call':<6} {'JSON':>10} {'GCF+dedup':>12} {'Bare refs':>10} {'vs JSON':>10}")
    print(f"{'='*72}")
    for i, (size, bare, is_gcf) in enumerate(results):
        json_size = json_baselines.get(i, 0)
        if json_size > 0:
            vs_json = f"-{100*(1-size/json_size):.0f}%"
        else:
            vs_json = "n/a"
        print(f"  {i+1:<4} {json_size:>10} {size:>12} {bare:>10} {vs_json:>10}")

    # Totals
    total_json = sum(json_baselines.get(i, 0) for i in range(len(results)))
    total_gcf = sum(size for size, _, _ in results)
    if total_json > 0:
        print(f"  {'-'*68}")
        print(f"  Total: {total_json:>7} JSON -> {total_gcf:>7} GCF+dedup = {100*(1-total_gcf/total_json):.0f}% fewer bytes")

    print(f"\n=== Proxy verbose output ===")
    cache_hits = 0
    min_size_bypasses = 0
    for line in stderr.strip().split("\n"):
        if "cache hit" in line.lower():
            cache_hits += 1
        if "gcf-proxy:" in line or "session" in line.lower() or "---" in line or "Tool calls" in line or "saved" in line or "Bytes" in line or "tokens" in line or "cache" in line.lower() or "Cache" in line:
            print(f"  {line}")

    # Count min-size bypasses (responses that weren't converted)
    for size, bare, is_gcf in results:
        if size > 0 and size < 100 and not is_gcf:
            min_size_bypasses += 1

    print(f"\n=== Feature verification ===")
    print(f"  Session dedup: {'PASS' if any(b > 0 for _, b, _ in results) else 'NO BARE REFS'}")
    print(f"  Cache hits: {cache_hits} {'(PASS)' if cache_hits > 0 else '(none detected)'}")
    print(f"  Min-size bypasses: {min_size_bypasses} {'(PASS)' if min_size_bypasses > 0 else '(none detected)'}")

if __name__ == "__main__":
    run()
