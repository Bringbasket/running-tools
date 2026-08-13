CREATE TABLE IF NOT EXISTS running_state (
    state_key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS running_state_updated_at
    ON running_state (updated_at DESC);
