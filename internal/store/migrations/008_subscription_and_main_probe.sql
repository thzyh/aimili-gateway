ALTER TABLE main_egress ADD COLUMN candidate_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (candidate_latency_ms >= 0);
ALTER TABLE main_egress ADD COLUMN vless_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (vless_latency_ms >= 0);
ALTER TABLE main_egress ADD COLUMN socks_latency_ms INTEGER NOT NULL DEFAULT 0 CHECK (socks_latency_ms >= 0);
ALTER TABLE main_egress ADD COLUMN last_checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE main_egress ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64);

CREATE TABLE gateway_subscription (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name = 'aimili-gateway-subscription'),
    client_id INTEGER NOT NULL DEFAULT 0 CHECK (client_id >= 0),
    subscription_id TEXT NOT NULL DEFAULT '' CHECK (length(subscription_id) <= 256),
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO gateway_subscription(id, resource_name, updated_at)
VALUES(1, 'aimili-gateway-subscription', 0);
