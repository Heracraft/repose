# A stand-in for Cloud Hypervisor in host-services (a real guest would be
# a third level of virtualisation): it serves the API socket and behaves
# as CH 53 does on woker-1 (DECISIONS I-650). SIGTERM ends it at once,
# with nothing written; PUT /api/v1/vm.power-button makes "the guest"
# shut down cleanly: its last write is synced, then it exits 0.
import http.server
import os
import socketserver
import sys
import threading
import time

d = sys.argv[1]
sock = os.path.join(d, "ch.sock")
try:
    os.unlink(sock)
except FileNotFoundError:
    pass
log = open(os.path.join(d, "writes.log"), "a")
log.write("started\n")
log.flush()
os.fsync(log.fileno())


class Handler(http.server.BaseHTTPRequestHandler):
    def do_PUT(self):
        if self.path != "/api/v1/vm.power-button":
            self.send_response(404)
            self.end_headers()
            return
        self.send_response(204)
        self.end_headers()

        def power_off():
            time.sleep(1)  # the guest's own shutdown
            log.write("powered off by the power button\n")
            log.flush()
            os.fsync(log.fileno())
            os._exit(0)

        threading.Thread(target=power_off, daemon=True).start()

    def log_message(self, *args):
        pass


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    pass


Server(sock, Handler).serve_forever()
