ALTER TABLE proxy_groups ADD COLUMN vless_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (vless_inbound_id >= 0);
ALTER TABLE proxy_groups ADD COLUMN mixed_inbound_id INTEGER NOT NULL DEFAULT 0 CHECK (mixed_inbound_id >= 0);
ALTER TABLE proxy_groups ADD COLUMN reality_public_key TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_groups ADD COLUMN reality_short_id TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_groups ADD COLUMN reality_server_name TEXT NOT NULL DEFAULT '';
