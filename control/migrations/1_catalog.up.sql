-- Catalog and observed state live here, not in YAML.

CREATE TABLE frontends (
    kind            TEXT PRIMARY KEY,
    socket_path     TEXT NOT NULL,
    observed_state  TEXT NOT NULL DEFAULT 'stopped',
    last_error      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE models (
    id              TEXT PRIMARY KEY,
    frontend        TEXT NOT NULL REFERENCES frontends(kind),
    path            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    desired_state   TEXT NOT NULL DEFAULT 'unloaded',
    observed_state  TEXT NOT NULL DEFAULT 'unloaded',
    last_error      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE model_events (
    id          UUID PRIMARY KEY,
    model       TEXT,
    event       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    details     JSONB
);

INSERT INTO frontends (kind, socket_path, observed_state) VALUES
    ('llama', 'unix/llama/llama.sock', 'stopped'),
    ('mlxcel', 'unix/mlxcel/mlxcel.sock', 'stopped');
