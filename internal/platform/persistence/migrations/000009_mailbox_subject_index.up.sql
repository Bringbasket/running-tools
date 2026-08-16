CREATE INDEX IF NOT EXISTS mailbox_messages_account_subject_lower
    ON mailbox_messages (account_id, LOWER(subject));
