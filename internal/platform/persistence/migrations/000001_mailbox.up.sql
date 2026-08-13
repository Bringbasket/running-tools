CREATE TABLE IF NOT EXISTS mailbox_sync_states (
    id BIGSERIAL PRIMARY KEY,
    key VARCHAR(64) NOT NULL UNIQUE,
    status JSONB NOT NULL DEFAULT '{}'::jsonb,
    highest_uid BIGINT NOT NULL DEFAULT 0 CHECK (highest_uid >= 0),
    allowed_aliases JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE TABLE IF NOT EXISTS mailbox_messages (
    id BIGSERIAL PRIMARY KEY,
    generation VARCHAR(128) NOT NULL,
    uid BIGINT NOT NULL CHECK (uid >= 0),
    aliases JSONB NOT NULL DEFAULT '[]'::jsonb,
    from_address VARCHAR(1000) NOT NULL DEFAULT '',
    subject VARCHAR(2000) NOT NULL DEFAULT '',
    message_date DOUBLE PRECISION NOT NULL,
    text TEXT NOT NULL DEFAULT '',
    safe_html TEXT NOT NULL DEFAULT '',
    codes JSONB NOT NULL DEFAULT '[]'::jsonb,
    partner_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
    CONSTRAINT mailbox_messages_generation_uid_key UNIQUE (generation, uid)
);

CREATE INDEX IF NOT EXISTS mailbox_messages_generation_message_date
    ON mailbox_messages (generation, message_date DESC);
CREATE INDEX IF NOT EXISTS mailbox_messages_aliases_gin
    ON mailbox_messages USING GIN (aliases);

CREATE TABLE IF NOT EXISTS mailbox_hidden_messages (
    id BIGSERIAL PRIMARY KEY,
    generation VARCHAR(128) NOT NULL,
    alias VARCHAR(320) NOT NULL,
    uid BIGINT NOT NULL CHECK (uid >= 0),
    CONSTRAINT mailbox_hidden_messages_generation_alias_uid_key UNIQUE (generation, alias, uid)
);

CREATE INDEX IF NOT EXISTS mailbox_hidden_messages_generation_uid
    ON mailbox_hidden_messages (generation, uid);
