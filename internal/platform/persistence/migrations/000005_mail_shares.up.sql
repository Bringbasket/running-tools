CREATE TABLE IF NOT EXISTS mail_share_links (
    id VARCHAR(64) PRIMARY KEY,
    account_id VARCHAR(64) NOT NULL,
    alias VARCHAR(320) NOT NULL,
    token_hash CHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS mail_share_links_account_alias_created
    ON mail_share_links (account_id, alias, created_at DESC);

CREATE TABLE IF NOT EXISTS mail_share_sessions (
    token_hash CHAR(64) PRIMARY KEY,
    account_id VARCHAR(64) NOT NULL,
    link_id VARCHAR(64) NOT NULL REFERENCES mail_share_links(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mail_share_sessions_account_expiry
    ON mail_share_sessions (account_id, expires_at);
