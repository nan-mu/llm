-- native_id is what unix.Load/Unload pass to the frontend.
-- Seed: reside fun-asr + HY-MT2 on llama; Gemma stays unloaded so mlxcel is not started.

ALTER TABLE models ADD COLUMN native_id TEXT NOT NULL;

ALTER TABLE models
    ADD CONSTRAINT models_frontend_check CHECK (frontend IN ('llama', 'mlxcel')),
    ADD CONSTRAINT models_purpose_check CHECK (purpose IN ('asr', 'translation')),
    ADD CONSTRAINT models_desired_state_check CHECK (desired_state IN ('unloaded', 'loaded')),
    ADD CONSTRAINT models_observed_state_check CHECK (observed_state IN ('unloaded', 'loading', 'loaded', 'unloading', 'failed'));

ALTER TABLE frontends
    ADD CONSTRAINT frontends_observed_state_check CHECK (observed_state IN ('stopped', 'starting', 'ready', 'stopping', 'failed'));

INSERT INTO models (id, frontend, path, purpose, native_id, desired_state, observed_state) VALUES
    ('fun-asr-nano-2512-q8_0', 'llama', 'models/fun-asr-nano-2512-q8_0.gguf', 'asr', 'fun-asr-nano-2512-q8_0', 'loaded', 'unloaded'),
    ('HY-MT2-7B-Q8_0', 'llama', 'models/HY-MT2-7B-Q8_0.gguf', 'translation', 'HY-MT2-7B-Q8_0', 'loaded', 'unloaded'),
    ('translategemma-12b-it-6bit', 'mlxcel', 'models/translategemma-12b-it-6bit', 'translation', 'translategemma-12b-it-6bit', 'unloaded', 'unloaded');
