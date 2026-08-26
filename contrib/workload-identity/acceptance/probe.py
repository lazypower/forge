# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT

import json
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

captured = None


class Probe(BaseHTTPRequestHandler):
    def do_POST(self):
        global captured
        if self.path != "/capture":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        candidate = json.loads(self.rfile.read(length))
        if set(candidate) != {"url", "token"}:
            self.send_error(400)
            return
        captured = candidate
        self.send_response(204)
        self.end_headers()

    def do_GET(self):
        if self.path != "/replay" or captured is None:
            self.send_error(404)
            return
        request = urllib.request.Request(
            captured["url"] + "&audience=vault",
            headers={"Authorization": "Bearer " + captured["token"]},
        )
        try:
            with urllib.request.urlopen(request) as response:
                status = response.status
        except urllib.error.HTTPError as error:
            status = error.code
        body = json.dumps({"status": status}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        return


ThreadingHTTPServer(("0.0.0.0", 8080), Probe).serve_forever()
