-- Catalog, purpose contracts, and OpenAI route enablement.
-- Authoritative model residency lives here, not in a YAML file.

CREATE TABLE frontends (
    kind            TEXT PRIMARY KEY,
    socket_path     TEXT,
    observed_state  TEXT NOT NULL DEFAULT 'stopped',
    last_error      TEXT,
    pids            BIGINT[],
    memory_mb       BIGINT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT frontends_observed_state_check
        CHECK (observed_state IN ('stopped', 'starting', 'loading', 'ready', 'stopping', 'failed'))
);

CREATE TABLE models (
    id                   TEXT PRIMARY KEY,
    frontend             TEXT NOT NULL REFERENCES frontends(kind),
    path                 TEXT NOT NULL,
    purpose              TEXT NOT NULL,
    native_id            TEXT NOT NULL,
    desired_state        TEXT NOT NULL DEFAULT 'unloaded',
    observed_state       TEXT NOT NULL DEFAULT 'unloaded',
    last_error           TEXT,
    pid                  BIGINT,
    restart_policy       TEXT NOT NULL DEFAULT 'unless-stopped',
    restart_max_retries  INT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT models_frontend_check CHECK (frontend IN ('llama', 'mlxcel', 'mlxlm')),
    CONSTRAINT models_purpose_check CHECK (purpose IN ('asr', 'translation', 'structured_translation')),
    CONSTRAINT models_desired_state_check CHECK (desired_state IN ('unloaded', 'loaded')),
    CONSTRAINT models_observed_state_check CHECK (observed_state IN ('unloaded', 'loading', 'loaded', 'unloading', 'failed')),
    CONSTRAINT models_restart_policy_check CHECK (restart_policy IN ('no', 'on-failure', 'unless-stopped', 'always'))
);

CREATE TABLE model_events (
    id          UUID PRIMARY KEY,
    model       TEXT,
    event       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    details     JSONB
);

CREATE TABLE api_routes (
    route      TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (route, purpose),
    CONSTRAINT api_routes_purpose_check
        CHECK (purpose IN ('asr', 'translation', 'structured_translation'))
);

CREATE TABLE purpose_translation (
    id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    content_encoding TEXT NOT NULL DEFAULT 'plain_string'
        CHECK (content_encoding = 'plain_string'),
    allow_system_role BOOLEAN NOT NULL DEFAULT true,
    allow_multimodal_content BOOLEAN NOT NULL DEFAULT false,
    temperature_default DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    temperature_max     DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    top_p_default       DOUBLE PRECISION NOT NULL DEFAULT 0.6,
    top_p_max           DOUBLE PRECISION NOT NULL DEFAULT 0.6,
    top_k_default       INT NOT NULL DEFAULT 20,
    top_k_max           INT NOT NULL DEFAULT 20,
    repetition_penalty_default DOUBLE PRECISION NOT NULL DEFAULT 1.05,
    repetition_penalty_max     DOUBLE PRECISION NOT NULL DEFAULT 1.05,
    max_tokens_default  INT NOT NULL DEFAULT 4096,
    max_tokens_max      INT NOT NULL DEFAULT 4096,
    inject_top_k BOOLEAN NOT NULL DEFAULT true,
    inject_repetition_penalty BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE purpose_structured_translation (
    id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    allowed_roles TEXT[] NOT NULL DEFAULT ARRAY['user', 'assistant'],
    deny_system_role BOOLEAN NOT NULL DEFAULT true,
    user_content_kind TEXT NOT NULL DEFAULT 'singleton_typed_part'
        CHECK (user_content_kind = 'singleton_typed_part'),
    user_content_len INT NOT NULL DEFAULT 1 CHECK (user_content_len = 1),
    allow_type_text  BOOLEAN NOT NULL DEFAULT true,
    allow_type_image BOOLEAN NOT NULL DEFAULT false,
    require_source_lang_code BOOLEAN NOT NULL DEFAULT true,
    require_target_lang_code BOOLEAN NOT NULL DEFAULT true,
    text_payload_field  TEXT NOT NULL DEFAULT 'text',
    image_payload_field TEXT NOT NULL DEFAULT 'url',
    lang_code_pattern TEXT NOT NULL
        DEFAULT '^(?:[a-z]{2}([_-][A-Za-z]{2})?)$',
    supported_lang_codes TEXT[],
    temperature_default DOUBLE PRECISION,
    temperature_max     DOUBLE PRECISION,
    max_tokens_default  INT,
    max_tokens_max      INT,
    require_chat_template BOOLEAN NOT NULL DEFAULT true,
    chat_template_id TEXT NOT NULL DEFAULT 'translategemma',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE purpose_asr (
    id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    route TEXT NOT NULL DEFAULT 'POST /v1/audio/transcriptions',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Filled when a supervised frontend is actually running. This tree does not sample.
CREATE TABLE frontend_memory_samples (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL REFERENCES frontends(kind),
    sampled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    memory_mb   BIGINT NOT NULL
);

CREATE INDEX frontend_memory_samples_kind_sampled_idx
    ON frontend_memory_samples (kind, sampled_at DESC);

INSERT INTO frontends (kind, socket_path, observed_state) VALUES
    ('llama', 'unix/llama', 'stopped'),
    ('mlxcel', 'unix/mlxcel/mlxcel.sock', 'stopped'),
    ('mlxlm', NULL, 'stopped');

-- Cold start: TranslateGemma desired=loaded on mlxlm. Others stay unloaded.
INSERT INTO models (id, frontend, path, purpose, native_id, desired_state, observed_state) VALUES
    ('fun-asr-nano-2512-q8_0', 'llama', 'models/fun-asr-nano-2512-q8_0.gguf', 'asr', 'fun-asr-nano-2512-q8_0', 'unloaded', 'unloaded'),
    ('HY-MT2-7B-Q8_0', 'llama', 'models/HY-MT2-7B-Q8_0.gguf', 'translation', 'HY-MT2-7B-Q8_0', 'unloaded', 'unloaded'),
    ('translategemma-12b-it-6bit', 'mlxlm', 'models/translategemma-12b-it-6bit', 'structured_translation', 'translategemma-12b-it-6bit', 'loaded', 'unloaded');

INSERT INTO api_routes (route, purpose, enabled) VALUES
    ('POST /v1/chat/completions', 'translation', false),
    ('POST /v1/translations', 'structured_translation', false),
    ('POST /v1/audio/transcriptions', 'asr', false);

INSERT INTO purpose_translation (id) VALUES (1);
INSERT INTO purpose_structured_translation (id) VALUES (1);
INSERT INTO purpose_asr (id) VALUES (1);
