# Agent constraints

Stable architecture decisions for this Encore app. Do not treat this as an end-user README.

## Services

Three Encore services:

- `gateway` — sole **business** OpenAI-compatible HTTP surface (`POST /v1/chat/completions`, `POST /v1/translations`, `GET /v1/models`, plus `/docs` and `/openapi.json`). Future: API tokens, sessions, usage.
- `control` — model catalog, observed residency/frontend state, frontend management, localhost gRPC Load/Unload. Narrow public HTTP only: `/control/health`, `/control/routes`. Do not expose gRPC, Load/Unload, sockets, or secrets via those pages.
- `unix` — private infer router only (`POST /unix/chat/:native_id`). Opaque prompt JSON in / out (`body` field for Encore S2S). **No middleware.** Dials mlxlm JSON-over-UDS. Does not Start/Load models.

Do not introduce services named `dataplane`, `identity`, `openai`, or `inference`.

## OpenAI routing (this slice)

- Request `model` is a **catalog id**. Gateway calls `control.GetSnapshot`, reads `purpose`, and checks `api_routes` enablement for that `(route, purpose)`.
- **`POST /v1/chat/completions`**: purpose=`translation` only (BabelDOC / HY-MT2). `messages[].content` is a **string**. Request fields: `model`, `messages`, `temperature`, `top_p`, `max_tokens`, `stream`. `structured_translation` models on chat → `model_purpose_mismatch`.
- **`POST /v1/translations`**: sole structured path (TranslateGemma / purpose=`structured_translation`). Document request (`source_language`, `target_language`, `context`, `glossaries`, `inputs`). Response `object=translation.batch` with `translations[].output` and `usage.input_tokens` / `output_tokens` / `total_tokens`. Gateway builds **prompt-only** JSON and calls `unix.Chat`; strips markdown fences into `translations[].output`.
- TranslateGemma: worker uses `unix/mlx_lm/translategemma_chat_template.jinja` when `chat_template_id=translategemma` in the prompt JSON. API additives (`document_title`, `glossary`, …) are fields on the typed content part; `text` stays source-only.
- `asr` → transcriptions (not implemented yet).
- Serialization contracts live in control singleton tables `purpose_translation`, `purpose_structured_translation`, `purpose_asr`. Gateway reads them only via private APIs (`GetPurpose*`); it must not query the control database.
- Do **not** hardcode sampling or message-shape rules by model id; only by `purpose` → purpose_* row. Translations apply `purpose_structured_translation` defaults server-side (no client sampling fields).
- Sampling semantics **A** (chat/translation): omitted field → table default; explicit value `> max` → 400; `≤ max` kept. Injected `top_k` / `repetition_penalty` are applied in validate middleware and forwarded toward the worker for llama; mlxlm path goes through `unix.Chat`.
- Chat validation runs in `gateway/validate` via `openaiValidate` middleware (`tag:openai`) on **public** APIs only. Infer path is `unix.Chat` (no middleware).
- Frontend matrix: `translation` → llama ChatProxy (temporary) or `unix.Chat` for mlxlm; `structured_translation` → `unix.Chat` only.
- `GET /v1/models` lists loaded **translation** models only.
- Gateway must not invent enablement by scanning `models`; use `control.RouteEnabled(route, purpose)` / `ListEnabledRoutes`.
- This slice: **no auth** on chat, translations, `/docs`, or `/openapi.json`.
- Hard catalog failures return OpenAI `{"error":{...}}` with **HTTP 4xx** and `X-Should-Retry: false`.
- Mid-flight worker death: mlxlm serializes Metal `generate`; control applies `restart_policy`. Gateway waits/reposts up to ~3 minutes for chat recovery.

## Frontends (library) + unix service

`frontend/` is a Go library (not an Encore service): `frontend/llama`, `frontend/mlxcel`, `frontend/mlxlm`. Each implements `frontend.Runtime`. control holds them in a `FrontendKind → Runtime` map. **Only `control` may Start / Stop / Load / Unload.**

`unix` Encore service dials workers only. Sock cwd: `frontend/mlxlm/{native_id}.sock`. Worker binary: `unix/mlx_lm/bin/mlx_lm_server`.

- **mlxlm**: one-shot **JSON-over-UDS** (not HTTP). One connection = one JSON request + one JSON response. Health: `{"op":"health"}` → `{"ok":true}`. Chat response: `{"content":"...","usage":{...}}`.
- **llama** (temporary until removed): HTTP-over-UDS; gateway still uses `llama.ChatProxy`.

`gateway` must not import Runtime/Start. For mlxlm inference it calls `unix.Chat` only.

## State machine

`internal/modelstate` is the contract between control and gateway: control writes; gateway only reads snapshots.

Load requires the frontend to be `READY`. That rule lives in `modelstate` (and control); workers do not implement catalog policy.

`encore run` / `initService` must not unconditionally Start workers. Construct runtimes, then **synchronously** reconcile `desired_state = 'loaded'` rows. No such rows means no processes.

Default catalog residency: TranslateGemma (`translategemma-12b-it-6bit`) `desired=loaded`; others `unloaded`. Cold `encore run` reconciles that row and starts mlxlm.

Authoritative catalog lives in the **control database**, not `config/models.yaml`.

## Tokens and sessions

- **API Token** validation hook: `gateway/validate.APIToken` (no-op this slice).
- Future: Sessions bind to one API token; **AI Token** = usage on a session step.

## Run and layout

No `scripts/*.sh`. Frontend processes start through `frontend` when control calls Start.

Prefer gateway `/docs` for operator-facing try-it. Do not restore the uptime template.

`models/` is local weights only and is gitignored.
