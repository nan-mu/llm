-- Per-purpose serialization contracts (singleton rows) and composite api_routes.

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
INSERT INTO purpose_translation (id) VALUES (1);

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
INSERT INTO purpose_structured_translation (id) VALUES (1);

CREATE TABLE purpose_asr (
    id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    route TEXT NOT NULL DEFAULT 'POST /v1/audio/transcriptions',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO purpose_asr (id) VALUES (1);

ALTER TABLE models DROP CONSTRAINT IF EXISTS models_purpose_check;
ALTER TABLE models
    ADD CONSTRAINT models_purpose_check
    CHECK (purpose IN ('asr', 'translation', 'structured_translation'));

UPDATE models
SET purpose = 'structured_translation', updated_at = NOW()
WHERE id = 'translategemma-12b-it-6bit';

-- Rebuild api_routes with composite primary key (route, purpose).
DROP TABLE api_routes;

CREATE TABLE api_routes (
    route      TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (route, purpose),
    CONSTRAINT api_routes_purpose_check
        CHECK (purpose IN ('asr', 'translation', 'structured_translation'))
);

INSERT INTO api_routes (route, purpose, enabled) VALUES
    ('POST /v1/chat/completions', 'translation', false),
    ('POST /v1/chat/completions', 'structured_translation', false),
    ('POST /v1/audio/transcriptions', 'asr', false);
