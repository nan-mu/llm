-- Add mlxlm frontend and cut TranslateGemma over from mlxcel.
-- mlxcel remains registered for explicit catalog rows; path inference uses mlxlm.

INSERT INTO frontends (kind, socket_path, observed_state) VALUES
    ('mlxlm', 'unix/mlxlm', 'stopped')
ON CONFLICT (kind) DO NOTHING;

ALTER TABLE models DROP CONSTRAINT IF EXISTS models_frontend_check;
ALTER TABLE models
    ADD CONSTRAINT models_frontend_check CHECK (frontend IN ('llama', 'mlxcel', 'mlxlm'));

UPDATE models
SET frontend = 'mlxlm', desired_state = 'loaded', updated_at = NOW()
WHERE id = 'translategemma-12b-it-6bit';
