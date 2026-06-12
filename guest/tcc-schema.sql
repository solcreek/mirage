-- Full system TCC.db schema as created by macOS 26 (captured from a real
-- install). Used to initialize a fresh image's empty TCC.db offline so we can
-- seed the Screen Recording grant without a running tccd. admin.value=32 is the
-- schema version tccd expects (a wrong/missing version makes tccd rebuild and
-- discard our grant).
CREATE TABLE IF NOT EXISTS admin (key TEXT PRIMARY KEY NOT NULL, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS policies (id INTEGER NOT NULL PRIMARY KEY, bundle_id TEXT NOT NULL, uuid TEXT NOT NULL, display TEXT NOT NULL, UNIQUE (bundle_id, uuid));
CREATE TABLE IF NOT EXISTS active_policy (client TEXT NOT NULL, client_type INTEGER NOT NULL, policy_id INTEGER NOT NULL, PRIMARY KEY (client, client_type), FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE INDEX IF NOT EXISTS active_policy_id ON active_policy(policy_id);
CREATE TABLE IF NOT EXISTS access (service TEXT NOT NULL, client TEXT NOT NULL, client_type INTEGER NOT NULL, auth_value INTEGER NOT NULL, auth_reason INTEGER NOT NULL, auth_version INTEGER NOT NULL, csreq BLOB, policy_id INTEGER, indirect_object_identifier_type INTEGER, indirect_object_identifier TEXT NOT NULL DEFAULT 'UNUSED', indirect_object_code_identity BLOB, flags INTEGER, last_modified INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)), pid INTEGER, pid_version INTEGER, boot_uuid TEXT NOT NULL DEFAULT 'UNUSED', last_reminded INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)), PRIMARY KEY (service, client, client_type, indirect_object_identifier), FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE TABLE IF NOT EXISTS access_overrides (service TEXT NOT NULL PRIMARY KEY);
CREATE TABLE IF NOT EXISTS expired (service TEXT NOT NULL, client TEXT NOT NULL, client_type INTEGER NOT NULL, csreq BLOB, last_modified INTEGER NOT NULL, expired_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)), PRIMARY KEY (service, client, client_type));
CREATE TABLE IF NOT EXISTS integrity_flag (key TEXT PRIMARY KEY NOT NULL, value INTEGER NOT NULL);
INSERT OR IGNORE INTO admin (key, value) VALUES ('version', 32);
INSERT OR IGNORE INTO integrity_flag (key, value) VALUES ('integrity_flag', 0);
