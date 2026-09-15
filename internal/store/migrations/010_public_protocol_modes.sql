CREATE TABLE proxy_groups_v10 (
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
    egress_source TEXT NOT NULL DEFAULT 'slot' CHECK (egress_source IN ('slot', 'main')),
    aimili_slot INTEGER NOT NULL UNIQUE CHECK (aimili_slot >= 0),
    public_port INTEGER NOT NULL UNIQUE CHECK (public_port BETWEEN 1 AND 65535),
    mixed_port INTEGER NOT NULL UNIQUE CHECK (mixed_port BETWEEN 1 AND 65535),
    exit_ip TEXT NOT NULL DEFAULT '',
    config_fingerprint TEXT NOT NULL DEFAULT '',
    public_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (public_inbound_id >= 0),
    mixed_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (mixed_inbound_id >= 0),
    reality_public_key TEXT NOT NULL DEFAULT '',
    reality_short_id TEXT NOT NULL DEFAULT '',
    reality_server_name TEXT NOT NULL DEFAULT '',
    reality_mldsa65_verify TEXT NOT NULL DEFAULT '' CHECK (length(reality_mldsa65_verify) <= 4096),
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

INSERT INTO proxy_groups_v10(
    id, resource_name, country_code, country_name, proxy_type, candidate_id,
    candidate_ip, candidate_latency_ms, vless_latency_ms, socks_latency_ms,
    status, egress_source, aimili_slot, public_port, mixed_port, exit_ip,
    config_fingerprint, public_inbound_id, mixed_inbound_id, reality_public_key,
    reality_short_id, reality_server_name, reality_mldsa65_verify,
    last_error_code, recovery_state, version, created_at, updated_at,
    last_checked_at, last_rotated_at, last_seen_at
)
SELECT id, resource_name, country_code, country_name, proxy_type, candidate_id,
    candidate_ip, candidate_latency_ms, vless_latency_ms, socks_latency_ms,
    status, egress_source, aimili_slot, vless_port, mixed_port, exit_ip,
    config_fingerprint, vless_inbound_id, mixed_inbound_id, reality_public_key,
    reality_short_id, reality_server_name, reality_mldsa65_verify,
    last_error_code, recovery_state, version, created_at, updated_at,
    last_checked_at, last_rotated_at, last_seen_at
FROM proxy_groups;

CREATE TABLE proxy_operations_v10 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    proxy_group_id TEXT NOT NULL REFERENCES proxy_groups_v10(id) ON DELETE CASCADE,
    operation TEXT NOT NULL CHECK (operation IN ('enable', 'check', 'rotate', 'disable', 'repair')),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'rolled_back', 'repair_required', 'failed')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER
);

INSERT INTO proxy_operations_v10
SELECT id, proxy_group_id, operation, phase, result, error_code, started_at, completed_at
FROM proxy_operations;

DROP TABLE proxy_operations;
DROP TABLE proxy_groups;
ALTER TABLE proxy_groups_v10 RENAME TO proxy_groups;
ALTER TABLE proxy_operations_v10 RENAME TO proxy_operations;
CREATE INDEX proxy_operations_group_started_idx ON proxy_operations(proxy_group_id, started_at DESC);

CREATE TABLE main_egress_v10 (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name = 'agw-main'),
    country_code TEXT NOT NULL DEFAULT '' CHECK (length(country_code) IN (0, 2)),
    country_name TEXT NOT NULL DEFAULT '',
    proxy_type TEXT NOT NULL DEFAULT 'datacenter' CHECK (proxy_type IN ('residential', 'datacenter')),
    candidate_id TEXT NOT NULL DEFAULT '' CHECK (length(candidate_id) <= 256),
    exit_ip TEXT NOT NULL DEFAULT '',
    public_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (public_inbound_id >= 0),
    mixed_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (mixed_inbound_id >= 0),
    public_port INTEGER NOT NULL DEFAULT 0 CHECK (public_port BETWEEN 0 AND 65535),
    mixed_port INTEGER NOT NULL DEFAULT 0 CHECK (mixed_port BETWEEN 0 AND 65535),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    candidate_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (candidate_latency_ms >= 0),
    vless_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (vless_latency_ms >= 0),
    socks_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (socks_latency_ms >= 0),
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO main_egress_v10(
    id, resource_name, country_code, country_name, proxy_type, exit_ip,
    public_inbound_id, mixed_inbound_id, public_port, mixed_port, enabled,
    candidate_latency_ms, vless_latency_ms, socks_latency_ms, last_checked_at,
    last_error_code, updated_at
)
SELECT id, resource_name, country_code, country_name, proxy_type, exit_ip,
    vless_inbound_id, mixed_inbound_id, vless_port, mixed_port, enabled,
    candidate_latency_ms, vless_latency_ms, socks_latency_ms, last_checked_at,
    last_error_code, updated_at
FROM main_egress;

DROP TABLE main_egress;
ALTER TABLE main_egress_v10 RENAME TO main_egress;

CREATE TABLE egress_protocol_modes (
    egress_id TEXT PRIMARY KEY CHECK (egress_id GLOB 'agw-*'),
    active_mode TEXT NOT NULL CHECK (active_mode IN (
        'vless_tcp_reality_vision',
        'vless_xhttp_reality',
        'hysteria2_quic_tls'
    )),
    desired_mode TEXT NOT NULL CHECK (desired_mode IN (
        'vless_tcp_reality_vision',
        'vless_xhttp_reality',
        'hysteria2_quic_tls'
    )),
    state TEXT NOT NULL CHECK (state IN (
        'ready',
        'switching',
        'subscription_pending',
        'rolling_back',
        'repair_required'
    )),
    last_operation_id TEXT NOT NULL DEFAULT '' CHECK (length(last_operation_id) <= 128),
    last_request_hash TEXT NOT NULL DEFAULT '' CHECK (length(last_request_hash) IN (0, 64)),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO egress_protocol_modes(
    egress_id, active_mode, desired_mode, state, version, updated_at
)
SELECT resource_name, 'vless_tcp_reality_vision', 'vless_tcp_reality_vision', 'ready', 1, updated_at
FROM main_egress
WHERE resource_name = 'agw-main';

INSERT INTO egress_protocol_modes(
    egress_id, active_mode, desired_mode, state, version, updated_at
)
SELECT id, 'vless_tcp_reality_vision', 'vless_tcp_reality_vision', 'ready', 1, updated_at
FROM proxy_groups
WHERE status IN ('ready', 'degraded', 'repair_required');

CREATE TABLE egress_operations (
    operation_id TEXT PRIMARY KEY CHECK (length(operation_id) BETWEEN 8 AND 128),
    egress_id TEXT NOT NULL CHECK (egress_id GLOB 'agw-*'),
    kind TEXT NOT NULL CHECK (kind IN ('main_assign', 'protocol_switch')),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    transaction_id TEXT NOT NULL DEFAULT '' CHECK (length(transaction_id) <= 128),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(egress_id, kind, request_hash)
);

CREATE INDEX egress_operations_egress_started_idx
ON egress_operations(egress_id, started_at DESC);
