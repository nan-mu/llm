# llm

Encore Go app for a local OpenAI-compatible gateway, a control-plane catalog, and a Zotero document API.

Architecture contract: [AGENT.md](AGENT.md). Agent instructions from `encore app create --llm-rules agentsmd`: [AGENTS.md](AGENTS.md).

The module path is `encore.app`, which is what `encore app create -l go` writes.

## Run

Docker must be running (Postgres). Then:

```bash
encore run
```

Public checks:

- `GET /health` — gateway
- `GET /control/health` — control
- `GET /control/routes` — route enablement
- `GET /v1/health` — zotero facade
- `GET /docs` and `GET /openapi.json` — operator HTTP document

BabelDOC execution is not wired. Document routes store tasks and publish `babeldoc-translate`; nothing runs `pixi` or `babeldoc`. The default catalog row for TranslateGemma stays `desired=loaded`. If `unix/mlx_lm/bin/mlx_lm_server` is not on the machine, startup records that on the model row and keeps serving.
