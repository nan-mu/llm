-- PID / memory columns; mlxlm has no catalog socket_path.
-- Memory samples: 24h @ 10s = 8640 rows per frontend (trimmed in app).

ALTER TABLE models ADD COLUMN IF NOT EXISTS pid BIGINT;

ALTER TABLE frontends ADD COLUMN IF NOT EXISTS pids BIGINT[];
ALTER TABLE frontends ADD COLUMN IF NOT EXISTS memory_mb BIGINT;

ALTER TABLE frontends ALTER COLUMN socket_path DROP NOT NULL;

UPDATE frontends
SET socket_path = NULL
WHERE kind = 'mlxlm';

CREATE TABLE IF NOT EXISTS frontend_memory_samples (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL REFERENCES frontends(kind),
    sampled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    memory_mb   BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS frontend_memory_samples_kind_sampled_idx
    ON frontend_memory_samples (kind, sampled_at DESC);
