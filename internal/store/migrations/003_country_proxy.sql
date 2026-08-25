CREATE TABLE proxy_groups (
    id TEXT PRIMARY KEY CHECK (id GLOB 'agw-*'),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name GLOB 'agw-*'),
    country_code TEXT NOT NULL CHECK (length(country_code) = 2),
    country_name TEXT NOT NULL DEFAULT '',
    proxy_type TEXT NOT NULL CHECK (proxy_type IN ('residential', 'datacenter')),
    status TEXT NOT NULL CHECK (status IN ('provisioning', 'ready', 'rotating', 'degraded', 'repair_required', 'disabling')),
    aimili_slot INTEGER NOT NULL UNIQUE CHECK (aimili_slot >= 0),
    vless_port INTEGER NOT NULL UNIQUE CHECK (vless_port BETWEEN 1 AND 65535),
    mixed_port INTEGER NOT NULL UNIQUE CHECK (mixed_port BETWEEN 1 AND 65535),
    exit_ip TEXT NOT NULL DEFAULT '',
    config_fingerprint TEXT NOT NULL DEFAULT '',
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    recovery_state TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    last_rotated_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(country_code, proxy_type)
);

CREATE TABLE encrypted_credentials (
    purpose TEXT PRIMARY KEY CHECK (length(purpose) BETWEEN 1 AND 64),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) > 0),
    key_version INTEGER NOT NULL DEFAULT 1 CHECK (key_version = 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE mixed_source_cidrs (
    prefix TEXT PRIMARY KEY CHECK (length(prefix) BETWEEN 3 AND 64),
    created_at INTEGER NOT NULL
);

CREATE TABLE proxy_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    proxy_group_id TEXT NOT NULL REFERENCES proxy_groups(id) ON DELETE CASCADE,
    operation TEXT NOT NULL CHECK (operation IN ('enable', 'check', 'rotate', 'disable', 'repair')),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'rolled_back', 'repair_required', 'failed')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER
);

CREATE INDEX proxy_operations_group_started_idx
    ON proxy_operations(proxy_group_id, started_at DESC);
