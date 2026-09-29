CREATE TABLE documents (
    id              TEXT PRIMARY KEY CHECK (id ~ '^[0-9a-f]{64}$'),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'down', 'error')),
    source_pdf      BYTEA NOT NULL,
    dual_pdf        BYTEA,
    dual_sha256     TEXT,
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    generation      BIGINT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);

CREATE INDEX documents_status_created_idx ON documents (status, created_at);
