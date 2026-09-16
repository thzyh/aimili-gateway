CREATE TABLE freesub_backup_connections (
    id TEXT PRIMARY KEY CHECK (id = 'agw-freesub'),
    candidate_id TEXT NOT NULL CHECK (length(candidate_id) BETWEEN 1 AND 256),
    country_code TEXT NOT NULL CHECK (length(country_code) = 2),
    protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'vmess', 'trojan', 'shadowsocks')),
    candidate_ip TEXT NOT NULL DEFAULT '',
    exit_ip TEXT NOT NULL DEFAULT '',
    socks_port INTEGER NOT NULL DEFAULT 0 CHECK (socks_port BETWEEN 0 AND 65535),
    public_port INTEGER NOT NULL DEFAULT 0 CHECK (public_port BETWEEN 0 AND 65535),
    xui_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (xui_inbound_id >= 0),
    runtime_pid INTEGER NOT NULL DEFAULT 0 CHECK (runtime_pid >= 0),
    status TEXT NOT NULL CHECK (status IN ('standby', 'provisioning', 'ready', 'degraded', 'repair_required', 'waiting_manual')),
    repair_attempts INTEGER NOT NULL DEFAULT 0 CHECK (repair_attempts BETWEEN 0 AND 1),
    failure_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(failure_fingerprint) <= 128),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    candidate_config_ciphertext BLOB NOT NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_checked_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE freesub_backup_operations (
    operation_id TEXT PRIMARY KEY CHECK (length(operation_id) BETWEEN 8 AND 128),
    connection_id TEXT NOT NULL REFERENCES freesub_backup_connections(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('check', 'replace', 'disable')),
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'failed', 'waiting_manual')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(connection_id, kind, request_hash)
);

CREATE INDEX freesub_backup_operations_started_idx
ON freesub_backup_operations(connection_id, started_at DESC);
