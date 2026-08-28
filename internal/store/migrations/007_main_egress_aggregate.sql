ALTER TABLE proxy_groups ADD COLUMN egress_source TEXT NOT NULL DEFAULT 'slot' CHECK (egress_source IN ('slot', 'main'));

CREATE TABLE main_egress (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name GLOB 'agw-*'),
    country_code TEXT NOT NULL DEFAULT '' CHECK (length(country_code) IN (0, 2)),
    country_name TEXT NOT NULL DEFAULT '',
    proxy_type TEXT NOT NULL DEFAULT 'datacenter' CHECK (proxy_type IN ('residential', 'datacenter')),
    exit_ip TEXT NOT NULL DEFAULT '',
    vless_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (vless_inbound_id >= 0),
    mixed_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (mixed_inbound_id >= 0),
    vless_port INTEGER NOT NULL DEFAULT 0 CHECK (vless_port BETWEEN 0 AND 65535),
    mixed_port INTEGER NOT NULL DEFAULT 0 CHECK (mixed_port BETWEEN 0 AND 65535),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE aggregate_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    resource_name TEXT NOT NULL UNIQUE CHECK (resource_name GLOB 'agw-*'),
    vless_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (vless_inbound_id >= 0),
    vless_port INTEGER NOT NULL DEFAULT 0 CHECK (vless_port BETWEEN 0 AND 65535),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO aggregate_config(id, resource_name, enabled, updated_at)
VALUES(1, 'agw-aggregate-vless', 1, 0);
