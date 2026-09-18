# Agent constraints

Stable architecture decisions for this Encore app. Do not treat this as an end-user README.

## Services

Two Encore services only:

- `gateway` — sole **business** OpenAI-compatible HTTP surface (`POST /v1/chat/completions`, `GET /v1/models`, plus `/docs` and `/openapi.json`). Future: API tokens, sessions, usage.
- `control` — model catalog, observed residency/frontend state, frontend management, localhost gRPC Load/Unload. Narrow public HTTP only: `/control/health`, `/control/routes`. Do not expose gRPC, Load/Unload, sockets, or secrets via those pages.

Do not introduce services named `dataplane`, `identity`, `openai`, or `inference`.

## OpenAI routing (this slice)

- Request `model` is a **catalog id**. Gateway calls `control.GetSnapshot`, reads `purpose`, and checks `api_routes` enablement. Do not add a dedicated `/translate` path.
- `purpose=translation` → chat completions; `purpose=asr` → transcriptions (not implemented yet).
- Gateway must not invent enablement by scanning `models`; use `control.RouteEnabled` / `ListEnabledRoutes`. `api_routes.enabled` flips after successful Load/Unload/reconcile when ≥1 model with that purpose is `observed_state=loaded`.
- Pass through `messages` (and other forwardable fields). **No** gateway translation system prompt.
- `purpose=translation` accepts OpenAI `message.content` as a **string only** (BabelDOC / plain text). Do not widen translation to multimodal `content` arrays (`image_url`, etc.). Multimodal chat needs a **new catalog purpose** (and matching route/enablement) designed separately — do not overload `translation`.
- This slice: **no auth** on chat, `/docs`, or `/openapi.json` (Bearer may be present and ignored).
- Hard catalog failures (`model_not_loaded`, `route_disabled`, purpose mismatch, unsupported frontend) return OpenAI `{"error":{...}}` with **HTTP 4xx** and `X-Should-Retry: false`. openai-python auto-retries all 5xx; BabelDOC only tenacity-retries `RateLimitError`, then **falls back per-paragraph and keeps the job running** — it will not abort the whole PDF job on API errors. Do not "fix" that by returning 503.
- Mid-flight worker death: mlxlm serializes Metal `generate` (queue concurrent BabelDOC calls); control applies catalog `restart_policy` (default `unless-stopped`) with backoff and a circuit break — Unload / `desired=unloaded` suppresses auto-restart; decision lives in control, not unix/gateway. Gateway waits/reposts for up to ~3 minutes so a single BabelDOC request (600s client timeout) can survive a crash+reload.

## Unix frontends

`unix/` is a Go library, not an Encore service (`unix/llama`, `unix/mlxcel`, `unix/mlxlm`). Each frontend implements `unix.Runtime`. control holds them in a `FrontendKind → Runtime` map; `modelstate.AllFrontends()` is the ordered registry. Adding a backend: implement `unix.Runtime` under `unix/<kind>/`, append to `AllFrontends`, insert a `frontends` row, register in `newRuntimes`. Do not add an Encore service.

Processes are started by `unix` via `os/exec`. **Only `control` may Start / Stop / Load / Unload.** `gateway` must not start a process or load a model as a side effect of inference (no implicit autoload). Launch argv must include `--no-models-autoload` and must not name a model to reside.

`gateway` may import `unix` only as an Inferencer / dial-only forwarder (e.g. `mlxlm.ChatProxy`). It must not receive a Runtime/Start surface.

llama.cpp, mlxcel, and mlxlm expose **no** public web or OpenAI HTTP; they listen for internal HTTP on Unix sockets. Only `gateway` may emit OpenAI-compatible business HTTP.

Socket cwd lives in `unix/llama/`, `unix/mlxcel/`, and `unix/mlxlm/` (mlxlm: one sock per model `{cwd}/{native_id}.sock`). Do not put those dirs at the repo root.

## State machine

`internal/modelstate` is the contract between control and gateway: control writes (applies transitions); gateway only reads snapshots. Gateway must not bypass the state machine to run management protocol.

Load requires the frontend to be `READY`. That rule lives in `modelstate` (and later control); `unix` does not implement catalog policy.

`encore run` / `initService` must not unconditionally Start llama-server or mlxcel-server. Construct runtimes, then **synchronously** reconcile `desired_state = 'loaded'` rows grouped by `frontend`. No such rows means no processes. Start a frontend only to Load a model into it. If any catalog-desired Load or frontend Start fails, drain frontends and fail `initService` so the process exits. A later gRPC `LoadModel` failure returns the error to the caller and must not exit the process.

`Load` / `Unload` persist `desired_state`. On boot, observed state is reset (process is new) and reconcile follows the catalog. Unload of a frontend's last loaded model Stops that frontend. Unloading one of several models on the same frontend leaves the process running.

When the Encore app exits (`Shutdown`, including `encore run` Ctrl+C), control Unloads every model the frontends still report as loaded, then SIGTERM/SIGKILL llama-server and mlxcel-server (including instance children and a leftover process holding the Unix socket).

Default catalog residency: `translategemma-12b-it-6bit` desired=loaded (mlxlm). `fun-asr-nano-2512-q8_0` and `HY-MT2-7B-Q8_0` stay desired=unloaded so llama-server is not started. The llama frontend remains in the tree for a later GGUF→MLX move.

Authoritative catalog and observed model/frontend state live in the **control database**, not `config/models.yaml`. Do not read YAML on the normal path. At most two future gRPC methods: import a YAML file into the tables, export the tables to a file.

## Tokens and sessions

Future (not this slice):

- **API Token**: `sk-local-...` credential. Sessions bind to one API token.
- **AI Token**: `prompt_tokens` / `completion_tokens` / `total_tokens` on a session step.

A request that targets a session with a different API token must be rejected. Usage is two SQL views: totals per session, then totals per API token across that token's sessions.

## Run and layout

No `scripts/*.sh`. cargo-make (ninja internally) is for **compiling** llama-server / mlxcel-server later, not for starting them. Frontend processes are started through `unix` when control calls Start.

Locally, `encore run` is enough for the Encore app. Prefer gateway `/docs` for operator-facing API try-it — not the Encore Local Dashboard (`:9400`) as the product surface. `ChatCompletions` is typed (panel forms); business errors return OpenAI `{"error":{...}}` with HTTP status set by `openaiHTTPStatus` middleware (returning `*errs.Error` would force Encore's `{code,message}` envelope).

Do not restore the uptime template (`slack`, `site`, `monitor`, `frontend`).

`models/` is local weights only and is gitignored. Encore does not parse weight files.

`/Users/nan/llm-backup` is read-only. Do not modify it.
