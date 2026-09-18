# llm

Local inference gateway on Apple Silicon. Encore provides the only OpenAI-compatible HTTP (`gateway`) and a control plane (`control`). llama.cpp and mlxcel speak Unix sockets only.

Architecture constraints for agents: [AGENT.md](AGENT.md).

## Run

Docker must be running (Postgres). Then:

```bash
encore run
```

Health:

- `GET /health` — gateway
- `GET /control/health` — control

OpenAI routes, sessions, and gRPC are not wired yet. `unix/llama` and `unix/mlxcel` can start the frontend processes and Load a model; only control should call that, and it is not connected in this slice.
