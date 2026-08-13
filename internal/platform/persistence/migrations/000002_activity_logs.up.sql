CREATE TABLE IF NOT EXISTS activity_logs (
    id BIGSERIAL PRIMARY KEY,
    module VARCHAR(64) NOT NULL,
    category VARCHAR(64) NOT NULL,
    action VARCHAR(128) NOT NULL,
    level VARCHAR(16) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    summary VARCHAR(500) NOT NULL,
    source VARCHAR(32) NOT NULL,
    method VARCHAR(16) NOT NULL DEFAULT '',
    path VARCHAR(500) NOT NULL DEFAULT '',
    http_status INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    detail VARCHAR(2000) NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS activity_logs_module_created_at
    ON activity_logs (module, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_logs_module_category_created_at
    ON activity_logs (module, category, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_logs_module_level_created_at
    ON activity_logs (module, level, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_logs_request_id
    ON activity_logs (request_id);
