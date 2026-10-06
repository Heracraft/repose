"""A stdio MCP server for the registry test (DECISIONS I-555).

One tool, `probe`, answers with the sha256 of the PROBE_TOKEN environment
variable and of the first argument, so a test sees that a secret reached
the server without the value ever being printed. With a second argument
(a sha256), the server exits before the handshake unless both digests
equal it, so an agent that lists the server as connected started it with
the secret in its environment and its arguments.
"""

import hashlib
import json
import os
import sys


def digest(v):
    return hashlib.sha256((v or "").encode()).hexdigest() if v else "unset"


def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else ""
    if len(sys.argv) > 2:
        want = sys.argv[2]
        if digest(os.environ.get("PROBE_TOKEN")) != want or digest(arg) != want:
            sys.stderr.write("probe: the secret did not arrive\n")
            sys.exit(1)
    for line in sys.stdin:
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if "id" not in msg:
            continue
        method = msg.get("method")
        if method == "initialize":
            result = {
                "protocolVersion": msg.get("params", {}).get("protocolVersion", "2025-06-18"),
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "probe", "version": "1"},
            }
        elif method == "tools/list":
            result = {"tools": [{"name": "probe", "description": "env digest", "inputSchema": {"type": "object"}}]}
        elif method == "tools/call":
            text = f"env={digest(os.environ.get('PROBE_TOKEN'))} arg={digest(arg)}"
            result = {"content": [{"type": "text", "text": text}]}
        else:
            result = {}
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": msg["id"], "result": result}) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
