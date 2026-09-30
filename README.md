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
