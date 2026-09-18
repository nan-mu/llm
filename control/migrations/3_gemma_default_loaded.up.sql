-- Default residency: TranslateGemma on mlxcel. Fun-ASR and HY-MT2 stay catalogued
-- but unloaded so llama-server is not started. llama frontend code remains.

UPDATE models
SET desired_state = 'unloaded', updated_at = NOW()
WHERE id IN ('fun-asr-nano-2512-q8_0', 'HY-MT2-7B-Q8_0');

UPDATE models
SET desired_state = 'loaded', updated_at = NOW()
WHERE id = 'translategemma-12b-it-6bit';
