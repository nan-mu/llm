-- Cold start must not pull any frontend; Load/Unload via gRPC only.
UPDATE models SET desired_state = 'unloaded', updated_at = NOW();

-- Llama is supervisor-style: per-model socks under this directory, not a shared sock.
UPDATE frontends SET socket_path = 'unix/llama' WHERE kind = 'llama';
