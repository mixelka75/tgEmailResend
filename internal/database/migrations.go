package database

const schema = `
CREATE TABLE IF NOT EXISTS email_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL,
    password TEXT NOT NULL,
    imap_server TEXT NOT NULL,
    chat_id INTEGER NOT NULL,
    topic_id INTEGER NOT NULL,
    is_active BOOLEAN DEFAULT true,
    last_uid INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by INTEGER NOT NULL,
    UNIQUE(chat_id, email),
    UNIQUE(chat_id, topic_id)
);

CREATE TABLE IF NOT EXISTS email_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    message_id TEXT,
    from_addr TEXT NOT NULL,
    from_name TEXT,
    subject TEXT,
    body_text TEXT,
    body_html TEXT,
    received_at DATETIME,
    is_read BOOLEAN DEFAULT false,
    is_deleted BOOLEAN DEFAULT false,
    telegram_msg_id INTEGER,
    detected_codes TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(account_id, uid)
);

CREATE INDEX IF NOT EXISTS idx_accounts_chat ON email_accounts(chat_id);
CREATE INDEX IF NOT EXISTS idx_accounts_active ON email_accounts(is_active);
CREATE INDEX IF NOT EXISTS idx_messages_account ON email_messages(account_id);
CREATE INDEX IF NOT EXISTS idx_messages_telegram ON email_messages(telegram_msg_id);
`
