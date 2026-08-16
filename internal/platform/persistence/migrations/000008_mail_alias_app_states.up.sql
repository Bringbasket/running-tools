CREATE TABLE IF NOT EXISTS mail_alias_app_states (
    id BIGSERIAL PRIMARY KEY,
    account_id VARCHAR(64) NOT NULL DEFAULT 'default',
    alias VARCHAR(320) NOT NULL,
    app_key VARCHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL,
    detected_at TIMESTAMPTZ,
    detected_uid BIGINT NOT NULL DEFAULT 0 CHECK (detected_uid >= 0),
    detected_subject VARCHAR(500) NOT NULL DEFAULT '',
    detected_sender VARCHAR(1000) NOT NULL DEFAULT '',
    confirmed_at TIMESTAMPTZ,
    confirmed_uid BIGINT NOT NULL DEFAULT 0 CHECK (confirmed_uid >= 0),
    confirmed_subject VARCHAR(500) NOT NULL DEFAULT '',
    confirmed_sender VARCHAR(1000) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT mail_alias_app_states_account_alias_app_key UNIQUE (account_id, alias, app_key),
    CONSTRAINT mail_alias_app_states_status_check CHECK (status IN ('observed', 'confirmed'))
);

CREATE INDEX IF NOT EXISTS mail_alias_app_states_account_status
    ON mail_alias_app_states (account_id, status, updated_at DESC);
