# llm

Encore Go app for a local OpenAI-compatible gateway and a control-plane catalog.

Architecture contract: [AGENT.md](AGENT.md). Agent instructions from `encore app create --llm-rules agentsmd`: [AGENTS.md](AGENTS.md).

The module path is `encore.app`, which is what `encore app create -l go` writes.

Zotero and BabelDOC PDF tasks live in a separate backend, not in this repo.

## Run

Docker must be running (Postgres). Then:

```bash
encore run
```

Public checks:

- `GET /health` — gateway
- `GET /control/health` — control
- `GET /control/routes` — route enablement
- `GET /docs` and `GET /openapi.json` — operator HTTP document
- `GET /bubblehub/health` — Bubble Hub

The default catalog row for TranslateGemma stays `desired=loaded`. If `unix/mlx_lm/bin/mlx_lm_server` is not on the machine, startup records that on the model row and keeps serving.
