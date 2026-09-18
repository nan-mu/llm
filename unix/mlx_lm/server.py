"""Thin single-model mlx-lm HTTP server over a Unix domain socket."""

from __future__ import annotations

import argparse
import json
import os
import signal
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from socketserver import UnixStreamServer
from typing import Any


class UnixHTTPServer(ThreadingHTTPServer, UnixStreamServer):
    address_family = UnixStreamServer.address_family


# MLX Metal is not safe for concurrent eval on one loaded model. Accept many
# HTTP connections, but run generate one-at-a-time so BabelDOC parallelism
# queues instead of SIGSEGV inside libmlx.
_infer_lock = threading.Lock()


def load_model(path: str):
    from mlx_lm import load

    model, tokenizer = load(path)
    return model, tokenizer


def apply_chat_template(tokenizer, messages: list[dict[str, str]]) -> str:
    if hasattr(tokenizer, "apply_chat_template"):
        try:
            return tokenizer.apply_chat_template(
                messages,
                tokenize=False,
                add_generation_prompt=True,
            )
        except Exception:
            pass
    parts: list[str] = []
    for m in messages:
        role = m.get("role", "user")
        content = m.get("content", "")
        parts.append(f"{role}: {content}")
    parts.append("assistant:")
    return "\n".join(parts)


def generate_reply(model, tokenizer, messages: list[dict[str, str]], max_tokens: int) -> str:
    from mlx_lm import generate

    prompt = apply_chat_template(tokenizer, messages)
    waited = not _infer_lock.acquire(blocking=False)
    if waited:
        sys.stderr.write("mlx_lm_server: infer lock busy, queuing generate\n")
        _infer_lock.acquire()
    try:
        return generate(model, tokenizer, prompt=prompt, max_tokens=max_tokens)
    finally:
        _infer_lock.release()


def make_handler(api_key: str | None, model, tokenizer, model_id: str):
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

        def _check_auth(self) -> bool:
            if not api_key:
                return True
            auth = self.headers.get("Authorization", "")
            expected = "Bearer " + api_key
            if auth != expected:
                self._json(401, {"error": {"message": "invalid api key", "type": "invalid_request_error"}})
                return False
            return True

        def _json(self, status: int, body: Any) -> None:
            raw = json.dumps(body).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            # Hard failures must not be retried by openai-python (retries all 5xx).
            if status >= 400:
                self.send_header("X-Should-Retry", "false")
            self.end_headers()
            self.wfile.write(raw)

        def _read_json(self) -> dict[str, Any] | None:
            length = int(self.headers.get("Content-Length", "0") or "0")
            raw = self.rfile.read(length) if length > 0 else b"{}"
            try:
                return json.loads(raw.decode("utf-8") or "{}")
            except json.JSONDecodeError:
                self._json(400, {"error": {"message": "invalid json", "type": "invalid_request_error"}})
                return None

        def do_GET(self) -> None:  # noqa: N802
            if self.path.split("?", 1)[0] != "/health":
                self.send_response(404)
                self.end_headers()
                return
            # Health is unauthenticated so supervisors can poll before auth wiring.
            self.send_response(200)
            self.end_headers()

        def do_POST(self) -> None:  # noqa: N802
            path = self.path.split("?", 1)[0]
            if path != "/v1/chat/completions":
                self.send_response(404)
                self.end_headers()
                return
            if not self._check_auth():
                return
            body = self._read_json()
            if body is None:
                return
            messages = body.get("messages")
            if not isinstance(messages, list) or not messages:
                self._json(400, {"error": {"message": "messages required", "type": "invalid_request_error"}})
                return
            if body.get("stream"):
                self._json(400, {"error": {"message": "streaming not supported", "type": "invalid_request_error"}})
                return
            max_tokens = int(body.get("max_tokens") or 2048)
            req_model = body.get("model") or model_id

            if model is None or tokenizer is None:
                content = "fakemlx-ok"
            else:
                try:
                    content = generate_reply(model, tokenizer, messages, max_tokens)
                except Exception as exc:  # noqa: BLE001
                    self._json(
                        500,
                        {"error": {"message": str(exc), "type": "server_error"}},
                    )
                    return

            resp = {
                "id": "chatcmpl-" + uuid.uuid4().hex[:24],
                "object": "chat.completion",
                "created": int(time.time()),
                "model": req_model,
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": content},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "total_tokens": 0,
                },
            }
            self._json(200, resp)

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
        help="Skip mlx_lm.load (health/chat stub for smoke / orphan tests)",
    )
    args = parser.parse_args()

    model_path = os.path.abspath(args.model_path)
    host = args.host
    if not os.path.isabs(host):
        host = os.path.abspath(host)

    model = None
    tokenizer = None
    model_id = os.path.basename(model_path.rstrip("/"))
    skip = args.skip_load or os.environ.get("MLXLM_SKIP_LOAD", "").strip() in ("1", "true", "yes")
    if not skip:
        if not os.path.isdir(model_path):
            sys.stderr.write(f"mlx_lm_server: model path not a directory: {model_path}\n")
            return 1
        sys.stderr.write(f"mlx_lm_server: loading {model_path}\n")
        model, tokenizer = load_model(model_path)
        sys.stderr.write("mlx_lm_server: model loaded\n")
    else:
        sys.stderr.write("mlx_lm_server: skip-load enabled\n")

    try:
        os.unlink(host)
    except FileNotFoundError:
        pass
    os.makedirs(os.path.dirname(host) or ".", exist_ok=True)

    api_key = args.api_key.strip() or None
    server = UnixHTTPServer(host, make_handler(api_key, model, tokenizer, model_id))

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
