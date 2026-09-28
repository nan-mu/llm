-- Cold start: TranslateGemma on mlxlm. Other catalog rows stay unloaded.
UPDATE models
SET desired_state = 'loaded', updated_at = NOW()
WHERE id = 'translategemma-12b-it-6bit';
