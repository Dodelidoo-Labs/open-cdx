#!/usr/bin/env python3
import argparse
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    server_version = "OpenCDXProof/1"

    def log_message(self, _format, *_args):
        # Never log the request, its headers, or its body.
        return

    def do_POST(self):
        length = min(int(self.headers.get("Content-Length", "0")), 64 * 1024 * 1024)
        remaining = length
        while remaining:
            chunk = self.rfile.read(min(remaining, 64 * 1024))
            if not chunk:
                break
            remaining -= len(chunk)
        authorized = self.headers.get("Authorization") == "Bearer signed-out-command-token"
        with open(self.server.result_path, "w", encoding="utf-8") as output:
            json.dump({"authorized": authorized, "path": self.path}, output)
        if not authorized:
            self.send_response(401)
            self.end_headers()
        else:
            body = (
                'event: response.created\n'
                'data: {"type":"response.created","response":{"id":"resp-proof"}}\n\n'
                'event: response.completed\n'
                'data: {"type":"response.completed","response":{"id":"resp-proof","usage":{"input_tokens":0,"input_tokens_details":null,"output_tokens":0,"output_tokens_details":null,"total_tokens":0}}}\n\n'
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        threading.Thread(target=self.server.shutdown, daemon=True).start()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port-file", required=True)
    parser.add_argument("--result-file", required=True)
    arguments = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.result_path = arguments.result_file
    with open(arguments.port_file, "w", encoding="utf-8") as output:
        output.write(str(server.server_port))
    server.serve_forever()


if __name__ == "__main__":
    main()
