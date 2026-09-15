ALTER TABLE proxy_groups
ADD COLUMN exit_ip_checked_at REAL NOT NULL DEFAULT 0
CHECK (exit_ip_checked_at >= 0);
