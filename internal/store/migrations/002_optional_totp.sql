CREATE TABLE admin_v2 (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL UNIQUE,
    password_hash BLOB NOT NULL CHECK (length(password_hash) > 0),
    totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1)),
    totp_secret_ciphertext BLOB,
    created_at INTEGER NOT NULL,
    security_updated_at INTEGER NOT NULL,
    CHECK (
        (totp_enabled = 0 AND totp_secret_ciphertext IS NULL) OR
        (totp_enabled = 1 AND length(totp_secret_ciphertext) > 0)
    )
);

INSERT INTO admin_v2(
    id,
    username,
    password_hash,
    totp_enabled,
    totp_secret_ciphertext,
    created_at,
    security_updated_at
)
SELECT
    id,
    username,
    password_hash,
    1,
    totp_secret_ciphertext,
    created_at,
    security_updated_at
FROM admin;

DROP TABLE admin;
ALTER TABLE admin_v2 RENAME TO admin;
