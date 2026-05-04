// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package users

// migrations is the ordered list of schema versions. Each entry is
// applied exactly once, in order, the first time the corresponding
// version is missing from the migrations table.
//
// Existing migrations MUST NOT be edited once released; add new
// migrations to the end.
var migrations = []string{
	// v1 — initial schema.
	`
CREATE TABLE IF NOT EXISTS users (
    id                          TEXT    PRIMARY KEY,                        -- UUIDv7
    name                        TEXT    NOT NULL UNIQUE,
    pubkey_b64                  TEXT    NOT NULL UNIQUE,                    -- base64 Noise XK static public
    created_at                  INTEGER NOT NULL,                            -- unix seconds
    expires_at                  INTEGER,                                     -- nullable
    quota_bytes_per_month       INTEGER,                                     -- nullable = unlimited
    used_bytes_current_month    INTEGER NOT NULL DEFAULT 0,
    quota_period_start          INTEGER NOT NULL,                            -- unix seconds
    last_seen                   INTEGER,
    status                      TEXT    NOT NULL DEFAULT 'active',           -- active|revoked|expired|quota_exceeded
    notes                       TEXT,
    tags                        TEXT                                         -- JSON array of strings
);
CREATE INDEX IF NOT EXISTS idx_users_pubkey ON users(pubkey_b64);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

CREATE TABLE IF NOT EXISTS admin_users (
    username        TEXT    PRIMARY KEY,
    password_hash   BLOB    NOT NULL,                                       -- bcrypt
    created_at      INTEGER NOT NULL
);
`,
}
