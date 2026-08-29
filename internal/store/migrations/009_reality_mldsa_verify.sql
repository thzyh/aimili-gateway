ALTER TABLE proxy_groups ADD COLUMN reality_mldsa65_verify TEXT NOT NULL DEFAULT '' CHECK (length(reality_mldsa65_verify) <= 4096);
