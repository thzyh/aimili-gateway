CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL UNIQUE,
    password_hash BLOB NOT NULL CHECK (length(password_hash) > 0),
    totp_secret_ciphertext BLOB NOT NULL CHECK (length(totp_secret_ciphertext) > 0),
    created_at INTEGER NOT NULL,
    security_updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    csrf_token_hash BLOB NOT NULL CHECK (length(csrf_token_hash) = 32),
    created_at INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    reauthenticated_at INTEGER,
    revoked_at INTEGER
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
    action TEXT NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    resource_type TEXT NOT NULL CHECK (length(resource_type) BETWEEN 1 AND 64),
    resource_fingerprint TEXT NOT NULL CHECK (length(resource_fingerprint) BETWEEN 1 AND 128),
    result TEXT NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events (created_at);

CREATE TABLE IF NOT EXISTS compatibility_status (
    service TEXT PRIMARY KEY,
    service_version TEXT NOT NULL DEFAULT '',
    adapter_version TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    health TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    checked_at INTEGER NOT NULL
);
