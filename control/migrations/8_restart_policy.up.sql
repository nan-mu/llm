-- Docker-aligned restart policy for model workers (decision lives in control).

ALTER TABLE models
  ADD COLUMN restart_policy TEXT NOT NULL DEFAULT 'unless-stopped'
    CHECK (restart_policy IN ('no', 'on-failure', 'unless-stopped', 'always')),
  ADD COLUMN restart_max_retries INT NULL;
