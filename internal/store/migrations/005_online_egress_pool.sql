CREATE TABLE proxy_groups_v5 (
    id TEXT PRIMARY KEY CHECK (id GLOB 'agw-*'),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name GLOB 'agw-*'),
    country_code TEXT NOT NULL CHECK (length(country_code) = 2),
    country_name TEXT NOT NULL DEFAULT '',
    proxy_type TEXT NOT NULL CHECK (proxy_type IN ('residential', 'datacenter')),
    candidate_id TEXT NOT NULL DEFAULT '' CHECK (length(candidate_id) <= 256),
    candidate_ip TEXT NOT NULL DEFAULT '',
    candidate_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (candidate_latency_ms >= 0),
    vless_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (vless_latency_ms >= 0),
    socks_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (socks_latency_ms >= 0),
    status TEXT NOT NULL CHECK (status IN ('provisioning', 'ready', 'rotating', 'degraded', 'repair_required', 'disabling')),
    aimili_slot INTEGER NOT NULL UNIQUE CHECK (aimili_slot >= 0),
    vless_port INTEGER NOT NULL UNIQUE CHECK (vless_port BETWEEN 1 AND 65535),
    mixed_port INTEGER NOT NULL UNIQUE CHECK (mixed_port BETWEEN 1 AND 65535),
    exit_ip TEXT NOT NULL DEFAULT '',
    config_fingerprint TEXT NOT NULL DEFAULT '',
    vless_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (vless_inbound_id >= 0),
    mixed_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (mixed_inbound_id >= 0),
    reality_public_key TEXT NOT NULL DEFAULT '',
    reality_short_id TEXT NOT NULL DEFAULT '',
    reality_server_name TEXT NOT NULL DEFAULT '',
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    recovery_state TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    last_rotated_at INTEGER NOT NULL DEFAULT 0,
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(country_code, proxy_type, candidate_id)
);

INSERT INTO proxy_groups_v5(
    id, resource_name, country_code, country_name, proxy_type, status,
    aimili_slot, vless_port, mixed_port, exit_ip, config_fingerprint,
    vless_inbound_id, mixed_inbound_id, reality_public_key, reality_short_id,
    reality_server_name, last_error_code, recovery_state, version, created_at,
    updated_at, last_checked_at, last_rotated_at
)
SELECT id, resource_name, country_code, country_name, proxy_type, status,
    aimili_slot, vless_port, mixed_port, exit_ip, config_fingerprint,
    vless_inbound_id, mixed_inbound_id, reality_public_key, reality_short_id,
    reality_server_name, last_error_code, recovery_state, version, created_at,
    updated_at, last_checked_at, last_rotated_at
FROM proxy_groups;

CREATE TABLE proxy_operations_v5 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    proxy_group_id TEXT NOT NULL REFERENCES proxy_groups_v5(id) ON DELETE CASCADE,
    operation TEXT NOT NULL CHECK (operation IN ('enable', 'check', 'rotate', 'disable', 'repair')),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'rolled_back', 'repair_required', 'failed')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER
);

INSERT INTO proxy_operations_v5
SELECT id, proxy_group_id, operation, phase, result, error_code, started_at, completed_at
FROM proxy_operations;

DROP TABLE proxy_operations;
DROP TABLE proxy_groups;
ALTER TABLE proxy_groups_v5 RENAME TO proxy_groups;
ALTER TABLE proxy_operations_v5 RENAME TO proxy_operations;
CREATE INDEX proxy_operations_group_started_idx ON proxy_operations(proxy_group_id, started_at DESC);
