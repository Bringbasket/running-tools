CREATE TABLE IF NOT EXISTS mail_accounts (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    apple_id VARCHAR(320) NOT NULL DEFAULT '',
    dsid VARCHAR(64) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO mail_accounts (id, name)
VALUES ('default', '默认账号')
ON CONFLICT (id) DO NOTHING;

ALTER TABLE activity_logs ADD COLUMN IF NOT EXISTS account_id VARCHAR(64) NOT NULL DEFAULT 'default';
ALTER TABLE mailbox_sync_states ADD COLUMN IF NOT EXISTS account_id VARCHAR(64) NOT NULL DEFAULT 'default';
ALTER TABLE mailbox_messages ADD COLUMN IF NOT EXISTS account_id VARCHAR(64) NOT NULL DEFAULT 'default';
ALTER TABLE mailbox_hidden_messages ADD COLUMN IF NOT EXISTS account_id VARCHAR(64) NOT NULL DEFAULT 'default';

DROP INDEX IF EXISTS activity_logs_module_created_at;
DROP INDEX IF EXISTS activity_logs_module_category_created_at;
DROP INDEX IF EXISTS activity_logs_module_level_created_at;
CREATE INDEX IF NOT EXISTS activity_logs_module_account_created_at ON activity_logs (module, account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_logs_module_account_category_created_at ON activity_logs (module, account_id, category, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_logs_module_account_level_created_at ON activity_logs (module, account_id, level, created_at DESC);

ALTER TABLE mailbox_sync_states DROP CONSTRAINT IF EXISTS mailbox_sync_states_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS mailbox_sync_states_account_key ON mailbox_sync_states (account_id, key);

ALTER TABLE mailbox_messages DROP CONSTRAINT IF EXISTS mailbox_messages_generation_uid_key;
CREATE UNIQUE INDEX IF NOT EXISTS mailbox_messages_account_generation_uid ON mailbox_messages (account_id, generation, uid);
CREATE INDEX IF NOT EXISTS mailbox_messages_account_generation_date ON mailbox_messages (account_id, generation, message_date DESC);

ALTER TABLE mailbox_hidden_messages DROP CONSTRAINT IF EXISTS mailbox_hidden_messages_generation_alias_uid_key;
CREATE UNIQUE INDEX IF NOT EXISTS mailbox_hidden_messages_account_generation_alias_uid ON mailbox_hidden_messages (account_id, generation, alias, uid);
CREATE INDEX IF NOT EXISTS mailbox_hidden_messages_account_generation_uid ON mailbox_hidden_messages (account_id, generation, uid);
