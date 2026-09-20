-- Split structured_translation onto POST /v1/translations; chat is translation-only.

DELETE FROM api_routes
WHERE route = 'POST /v1/chat/completions' AND purpose = 'structured_translation';

INSERT INTO api_routes (route, purpose, enabled)
VALUES ('POST /v1/translations', 'structured_translation', false)
ON CONFLICT (route, purpose) DO NOTHING;
