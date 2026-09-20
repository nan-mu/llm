"""Thin single-model mlx-lm JSON-over-UDS server (one connection = one request)."""

from __future__ import annotations

import argparse
import json
import os
import signal
import socket
import sys
import threading
from pathlib import Path
from typing import Any


# MLX Metal is not safe for concurrent eval on one loaded model.
_infer_lock = threading.Lock()

_TRANSLATEGEMMA_TEMPLATE = Path(__file__).with_name("translategemma_chat_template.jinja").read_text(
    encoding="utf-8"
)


def load_model(path: str):
    from mlx_lm import load

    model, tokenizer = load(path)
    return model, tokenizer


def apply_chat_template(tokenizer, messages: list[dict[str, Any]], *, structured: bool = False) -> str:
    if hasattr(tokenizer, "apply_chat_template"):
        try:
            kwargs: dict[str, Any] = {
                "tokenize": False,
                "add_generation_prompt": True,
            }
            if structured:
                kwargs["chat_template"] = _TRANSLATEGEMMA_TEMPLATE
            return tokenizer.apply_chat_template(messages, **kwargs)
        except Exception:
            if structured:
                raise
    if structured:
        raise ValueError("translategemma chat template required but apply_chat_template failed")
    parts: list[str] = []
    for m in messages:
        role = m.get("role", "user")
        content = m.get("content", "")
        if not isinstance(content, str):
            content = json.dumps(content, ensure_ascii=False)
        parts.append(f"{role}: {content}")
    parts.append("assistant:")
    return "\n".join(parts)


def generate_reply(
    model,
    tokenizer,
    messages: list[dict[str, Any]],
    max_tokens: int,
    *,
    structured: bool = False,
) -> tuple[str, dict[str, int]]:
    from mlx_lm import generate

    prompt = apply_chat_template(tokenizer, messages, structured=structured)
    prompt_tokens = count_tokens(tokenizer, prompt)
    waited = not _infer_lock.acquire(blocking=False)
    if waited:
        sys.stderr.write("mlx_lm_server: infer lock busy, queuing generate\n")
        _infer_lock.acquire()
    try:
        text = generate(model, tokenizer, prompt=prompt, max_tokens=max_tokens)
    finally:
        _infer_lock.release()
    completion_tokens = count_tokens(tokenizer, text, add_special_tokens=False)
    usage = {
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": prompt_tokens + completion_tokens,
    }
    return text, usage


def count_tokens(tokenizer, text: str, *, add_special_tokens: bool = True) -> int:
    if text is None:
        return 0
    s = text if isinstance(text, str) else str(text)
    if not s:
        return 0
    try:
        if hasattr(tokenizer, "encode"):
            ids = tokenizer.encode(s, add_special_tokens=add_special_tokens)
            return len(ids)
    except TypeError:
        try:
            ids = tokenizer.encode(s)
            return len(ids)
        except Exception:  # noqa: BLE001
            pass
    except Exception:  # noqa: BLE001
        pass
    try:
        out = tokenizer(s, add_special_tokens=add_special_tokens)
        ids = out["input_ids"] if isinstance(out, dict) else out.input_ids
        return len(ids)
    except Exception:  # noqa: BLE001
        return 0


def _messages_have_image(messages: list[dict[str, Any]]) -> bool:
    for m in messages:
        content = m.get("content")
        if isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get("type") == "image":
                    return True
    return False


def handle_request(body: dict[str, Any], model, tokenizer) -> dict[str, Any]:
    op = (body.get("op") or "").strip()
    if op == "health" or body.get("ping") is True:
        return {"ok": True}

    messages = body.get("messages")
    if not isinstance(messages, list) or not messages:
        return {"error": {"message": "messages required", "type": "invalid_request_error"}}
    if body.get("stream"):
        return {"error": {"message": "streaming not supported", "type": "invalid_request_error"}}

    max_tokens = int(body.get("max_tokens") or 2048)
    template_id = str(body.get("chat_template_id") or "").strip()
    structured = template_id == "translategemma"

    if structured and _messages_have_image(messages):
        return {"error": {"message": "image content not supported yet", "type": "invalid_request_error"}}

    if model is None or tokenizer is None:
        content = "fakemlx-ok"
        usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
    else:
        try:
            raw_content, usage = generate_reply(
                model, tokenizer, messages, max_tokens, structured=structured
            )
        except Exception as exc:  # noqa: BLE001
            return {"error": {"message": str(exc), "type": "server_error"}}
        content = raw_content if isinstance(raw_content, str) else str(raw_content)

    return {"content": content, "usage": usage}


def serve_conn(conn: socket.socket, model, tokenizer) -> None:
    try:
        chunks: list[bytes] = []
        while True:
            buf = conn.recv(65536)
            if not buf:
                break
            chunks.append(buf)
        raw = b"".join(chunks)
        try:
            body = json.loads(raw.decode("utf-8") or "{}")
            if not isinstance(body, dict):
                raise ValueError("request must be a JSON object")
        except (json.JSONDecodeError, ValueError) as exc:
            resp = {"error": {"message": f"invalid json: {exc}", "type": "invalid_request_error"}}
        else:
            resp = handle_request(body, model, tokenizer)
        out = json.dumps(resp, ensure_ascii=False).encode("utf-8")
        conn.sendall(out)
    finally:
        try:
            conn.close()
        except OSError:
            pass


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
    parser = argparse.ArgumentParser(description="mlx-lm single-model JSON UDS server")
    parser.add_argument("--model-path", required=True)
    parser.add_argument("--host", required=True, help="Unix socket path")
    parser.add_argument("--api-key", default="", help="ignored (UDS trust); kept for argv compat")
    parser.add_argument("--parent-pid", type=int, default=0)
    parser.add_argument("--skip-load", action="store_true")
    args = parser.parse_args()

    model_path = os.path.abspath(args.model_path)
    host = args.host
    if not os.path.isabs(host):
        host = os.path.abspath(host)

    model = None
    tokenizer = None
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

    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(host)
    srv.listen(64)
    srv.settimeout(1.0)

    stop = threading.Event()
    parent_pid = args.parent_pid or os.getppid()
    threading.Thread(target=watch_parent, args=(parent_pid, stop), daemon=True).start()

    def handle_signal(signum, _frame):
        sys.stderr.write(f"mlx_lm_server: signal {signum}, shutting down\n")
        stop.set()

    signal.signal(signal.SIGTERM, handle_signal)
    signal.signal(signal.SIGINT, handle_signal)

    sys.stderr.write(f"mlx_lm_server: listening on {host} (parent-pid={parent_pid}, proto=json)\n")
    try:
        while not stop.is_set():
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            except OSError:
                if stop.is_set():
                    break
                raise
            threading.Thread(target=serve_conn, args=(conn, model, tokenizer), daemon=True).start()
    finally:
        stop.set()
        try:
            srv.close()
        except OSError:
            pass
        try:
            os.unlink(host)
        except FileNotFoundError:
            pass
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
