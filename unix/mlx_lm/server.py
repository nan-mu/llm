"""Thin single-model mlx-lm HTTP server over a Unix domain socket."""

from __future__ import annotations

import argparse
import os
import signal
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from socketserver import UnixStreamServer


class UnixHTTPServer(ThreadingHTTPServer, UnixStreamServer):
    address_family = UnixStreamServer.address_family


def load_model(path: str):
    from mlx_lm import load

    model, tokenizer = load(path)
    return model, tokenizer


def make_handler(api_key: str | None):
    class Handler(BaseHTTPRequestHandler):
        def address_string(self) -> str:
            # On AF_UNIX, client_address is a string path (or empty), not (host, port).
            addr = self.client_address
            if isinstance(addr, str):
                return addr or "unix"
            if isinstance(addr, tuple) and addr:
                return str(addr[0])
            return "unix"

        def log_message(self, fmt: str, *args) -> None:  # noqa: A003
            sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % args))

        def do_GET(self) -> None:  # noqa: N802
            if self.path.split("?", 1)[0] != "/health":
                self.send_response(404)
                self.end_headers()
                return
            # Health is unauthenticated so supervisors can poll before auth wiring.
            self.send_response(200)
            self.end_headers()

    return Handler


def watch_parent(parent_pid: int, stop: threading.Event) -> None:
    while not stop.wait(1.0):
        if parent_pid <= 1:
            continue
        try:
            os.kill(parent_pid, 0)
        except OSError:
            sys.stderr.write(f"mlx_lm_server: parent pid {parent_pid} gone, exiting\n")
            os._exit(0)


def main() -> int:
    parser = argparse.ArgumentParser(description="mlx-lm single-model UDS server")
    parser.add_argument("--model-path", required=True)
    parser.add_argument("--host", required=True, help="Unix socket path")
    parser.add_argument("--api-key", default="")
    parser.add_argument(
        "--parent-pid",
        type=int,
        default=0,
        help="Exit when this PID disappears (Go supervisor)",
    )
    parser.add_argument(
        "--skip-load",
        action="store_true",
        help="Skip mlx_lm.load (health-only smoke / orphan tests)",
    )
    args = parser.parse_args()

    model_path = os.path.abspath(args.model_path)
    host = args.host
    if not os.path.isabs(host):
        host = os.path.abspath(host)

    skip = args.skip_load or os.environ.get("MLXLM_SKIP_LOAD", "").strip() in ("1", "true", "yes")
    if not skip:
        if not os.path.isdir(model_path):
            sys.stderr.write(f"mlx_lm_server: model path not a directory: {model_path}\n")
            return 1
        sys.stderr.write(f"mlx_lm_server: loading {model_path}\n")
        load_model(model_path)
        sys.stderr.write("mlx_lm_server: model loaded\n")
    else:
        sys.stderr.write("mlx_lm_server: skip-load enabled\n")

    try:
        os.unlink(host)
    except FileNotFoundError:
        pass
    os.makedirs(os.path.dirname(host) or ".", exist_ok=True)

    api_key = args.api_key.strip() or None
    server = UnixHTTPServer(host, make_handler(api_key))

    stop = threading.Event()
    parent_pid = args.parent_pid or os.getppid()
    threading.Thread(target=watch_parent, args=(parent_pid, stop), daemon=True).start()

    def handle_signal(signum, _frame):
        sys.stderr.write(f"mlx_lm_server: signal {signum}, shutting down\n")
        stop.set()
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, handle_signal)
    signal.signal(signal.SIGINT, handle_signal)

    sys.stderr.write(f"mlx_lm_server: listening on {host} (parent-pid={parent_pid})\n")
    try:
        server.serve_forever()
    finally:
        stop.set()
        server.server_close()
        try:
            os.unlink(host)
        except FileNotFoundError:
            pass
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
