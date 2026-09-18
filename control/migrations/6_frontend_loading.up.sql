-- Allow frontend observed_state = loading (weights in flight after Start).

ALTER TABLE frontends DROP CONSTRAINT IF EXISTS frontends_observed_state_check;
ALTER TABLE frontends
    ADD CONSTRAINT frontends_observed_state_check
    CHECK (observed_state IN ('stopped', 'starting', 'loading', 'ready', 'stopping', 'failed'));
