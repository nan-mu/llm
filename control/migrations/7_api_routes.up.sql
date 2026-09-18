-- OpenAI route enablement: flipped by control after Load/Unload/reconcile.
-- Gateway reads enabled flags; it must not infer from models itself.

CREATE TABLE api_routes (
    route      TEXT PRIMARY KEY,
    purpose    TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT api_routes_purpose_check CHECK (purpose IN ('asr', 'translation'))
);

INSERT INTO api_routes (route, purpose, enabled) VALUES
    ('POST /v1/chat/completions', 'translation', false),
    ('POST /v1/audio/transcriptions', 'asr', false);
