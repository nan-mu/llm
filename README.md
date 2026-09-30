# llm

Local inference gateway on Apple Silicon. Encore provides OpenAI-compatible HTTP (`gateway`), a control plane (`control`), and a private unix infer router.

Architecture constraints for agents: [AGENT.md](AGENT.md).

## Run

Docker must be running (Postgres). Then:

```bash
encore run
```

Health:

- `GET /health` — gateway
- `GET /control/health` — control
- `GET /docs` and `GET /openapi.json` — operator HTTP document

Model residency (no auth yet):

- `GET /control/models`, `GET /control/models/:id`
- `PUT /control/models/:id/desired-state` with `{"state":"loaded"|"unloaded"}`
- `POST /control/models/:id/reloads`
- `GET /control/frontends`, `GET /control/frontends/:kind`

Control 管理面文档由 Encore 从源码注释自动生成（本地 Dashboard Service Catalog，或 `encore gen client --lang=openapi --services=control`）。
