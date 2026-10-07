"""The laptop's end of `repose mcp forward`, for the guest-base test.

usage: mcp-fake-laptop.py -- COMMAND [ARGS...]

Runs COMMAND (`repose-mcp hold NAME`) with its stdin and stdout as the
laptop's ssh would carry them, in the frames of internal/mcpshim/frame.go:
a type byte, a big-endian stream id and length, then the payload. Each
stream opened gets an in-process MCP server with one tool, `where`, which
answers "laptop". The shim's pings are answered here, as the CLI does.
Prints "ready <json>" for each R frame and "gone <name>" for a G frame,
flushed, and runs until killed.
"""

import json
import struct
import subprocess
import sys
import threading

PING = "$/repose/ping"
lock = threading.Lock()


def main():
    argv = sys.argv[1:]
    if not argv or argv[0] != "--" or len(argv) < 2:
        print(__doc__, file=sys.stderr)
        sys.exit(64)
    hold = subprocess.Popen(argv[1:], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    out = hold.stdin
    bufs = {}

    def send(typ, sid, payload=b""):
        with lock:
            out.write(struct.pack(">BII", typ, sid, len(payload)) + payload)
            out.flush()

    def reply(sid, mid, result):
        send(ord("D"), sid, (json.dumps({"jsonrpc": "2.0", "id": mid, "result": result}) + "\n").encode())

    def handle(sid, line):
        try:
            m = json.loads(line)
        except ValueError:
            return
        method, mid = m.get("method"), m.get("id")
        if mid is None:
            return
        if method == PING:
            reply(sid, mid, {})
        elif method == "initialize":
            reply(sid, mid, {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "fake-laptop", "version": "1"}})
        elif method == "tools/list":
            reply(sid, mid, {"tools": [{"name": "where", "description": "where this runs", "inputSchema": {"type": "object"}}]})
        elif method == "tools/call":
            reply(sid, mid, {"content": [{"type": "text", "text": "laptop"}]})
        else:
            reply(sid, mid, {})

    src = hold.stdout
    while True:
        head = src.read(9)
        if len(head) < 9:
            return
        typ, sid, n = struct.unpack(">BII", head)
        payload = src.read(n)
        t = chr(typ)
        if t == "O":
            bufs[sid] = b""
        elif t == "D" and sid in bufs:
            bufs[sid] += payload
            while b"\n" in bufs[sid]:
                line, bufs[sid] = bufs[sid].split(b"\n", 1)
                handle(sid, line)
        elif t == "C":
            bufs.pop(sid, None)
        elif t == "R":
            print("ready " + payload.decode(), flush=True)
        elif t == "G":
            print("gone " + payload.decode(), flush=True)


if __name__ == "__main__":
    main()
