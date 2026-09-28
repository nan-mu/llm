# llm

Local inference gateway on Apple Silicon. Encore provides OpenAI-compatible HTTP (`gateway`), a control plane (`control`), and Zotero↔BabelDOC translation (`zotero` / `babeldoc`).

Architecture constraints for agents: [AGENT.md](AGENT.md).

## Run

Docker must be running (Postgres). Then:

```bash
encore run
```

Health:

- `GET /health` — gateway
- `GET /control/health` — control
- `GET /v1/health` — zotero facade

### Zotero document API (no auth this slice)

Base URL: `http://127.0.0.1:4000`

| Method | Path | Notes |
|--------|------|--------|
| GET | `/v1/documents` | Full task list |
| POST | `/v1/documents` | Raw `application/pdf` + `X-Document-SHA256` |
| GET | `/v1/documents/:hash/files/dual` | Bilingual PDF + `X-Artifact-SHA256` |
| DELETE | `/v1/documents/:hash` | Idempotent cleanup |
| POST | `/v1/documents/:hash/retry` | Re-queue `error` tasks |

BabelDOC runs via `pixi` from `BABELDOC_ROOT` (default `/Users/nan/BabelDOC`); work dirs under `BABELDOC_WORK_ROOT` (default `/tmp`). Source and dual PDFs are also stored in the `babeldoc` Postgres database.
