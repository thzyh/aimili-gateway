CREATE TABLE account_sync_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    status TEXT NOT NULL CHECK (status IN ('reset_required', 'synced', 'checking', 'repair_required', 'incompatible')),
    username_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(username_fingerprint) <= 128),
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64)
);

INSERT INTO account_sync_state(id, status, username_fingerprint, last_checked_at, error_code)
VALUES(1, 'reset_required', '', 0, '');

CREATE TABLE account_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation TEXT NOT NULL CHECK (operation IN ('check', 'change', 'repair', 'rollback')),
    phase TEXT NOT NULL CHECK (length(phase) BETWEEN 1 AND 64),
    result TEXT NOT NULL CHECK (result IN ('running', 'success', 'rolled_back', 'repair_required', 'failed')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    started_at INTEGER NOT NULL,
    completed_at INTEGER
);

CREATE INDEX account_operations_started_idx ON account_operations(started_at DESC);

CREATE TABLE mixed_source_policy (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    apply_status TEXT NOT NULL CHECK (apply_status IN ('pending', 'applying', 'applied', 'failed', 'repair_required')),
    updated_at INTEGER NOT NULL
);

INSERT INTO mixed_source_policy(id, enabled, apply_status, updated_at)
VALUES(1, 1, 'pending', unixepoch('subsec') * 1000);
