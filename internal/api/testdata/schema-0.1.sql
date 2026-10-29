-- The database schema of Marineris 0.1.x (store migrations 1 to 5), frozen
-- for the upgrade test. Never edit: it stands for databases in the field.
CREATE TABLE schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version(version) VALUES (5);
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE users (
	id             INTEGER PRIMARY KEY,
	username       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash  TEXT NOT NULL,
	totp_secret    TEXT NOT NULL DEFAULT '',
	totp_last_step INTEGER NOT NULL DEFAULT 0,
	recovery_codes TEXT NOT NULL DEFAULT '[]',
	webauthn_id    BLOB NOT NULL,
	role           TEXT NOT NULL DEFAULT 'admin',
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL
);
CREATE TABLE webauthn_credentials (
	id           BLOB PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	data         TEXT NOT NULL,
	created_at   TIMESTAMP NOT NULL,
	last_used_at TIMESTAMP
);
CREATE TABLE sessions (
	id_hash      TEXT PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	csrf_token   TEXT NOT NULL,
	created_at   TIMESTAMP NOT NULL,
	last_seen_at TIMESTAMP NOT NULL,
	expires_at   TIMESTAMP NOT NULL,
	auth_at      TIMESTAMP NOT NULL,
	ip           TEXT NOT NULL DEFAULT '',
	user_agent   TEXT NOT NULL DEFAULT '',
	method       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE dns_profiles (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL UNIQUE COLLATE NOCASE,
	provider      TEXT NOT NULL,
	config        TEXT NOT NULL,
	created_at    TIMESTAMP NOT NULL,
	updated_at    TIMESTAMP NOT NULL,
	last_test_at  TIMESTAMP,
	last_test_ok  INTEGER NOT NULL DEFAULT 0,
	last_test_msg TEXT NOT NULL DEFAULT ''
);
CREATE TABLE domains (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
	dns_profile_id INTEGER REFERENCES dns_profiles(id) ON DELETE RESTRICT,
	wildcard       INTEGER NOT NULL DEFAULT 1,
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL
);
CREATE TABLE routes (
	id          INTEGER PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
	host        TEXT NOT NULL COLLATE NOCASE,
	path_prefix TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	target      TEXT NOT NULL,
	auth        TEXT NOT NULL DEFAULT 'authelia',
	rate_limit  TEXT NOT NULL DEFAULT '{}',
	enabled     INTEGER NOT NULL DEFAULT 1,
	created_at  TIMESTAMP NOT NULL,
	updated_at  TIMESTAMP NOT NULL,
	UNIQUE(host, path_prefix)
);
CREATE TABLE stacks (
	name        TEXT PRIMARY KEY,
	compose     TEXT NOT NULL,
	env         TEXT NOT NULL DEFAULT '',
	unsafe_ack  INTEGER NOT NULL DEFAULT 0,
	created_at  TIMESTAMP NOT NULL,
	updated_at  TIMESTAMP NOT NULL,
	deployed_at TIMESTAMP
);
CREATE TABLE config_history (
	id         INTEGER PRIMARY KEY,
	component  TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL,
	actor      TEXT NOT NULL,
	reason     TEXT NOT NULL,
	content    TEXT NOT NULL,
	status     TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX config_history_component ON config_history(component, id);
CREATE TABLE audit_log (
	id      INTEGER PRIMARY KEY,
	at      TIMESTAMP NOT NULL,
	actor   TEXT NOT NULL,
	ip      TEXT NOT NULL,
	action  TEXT NOT NULL,
	target  TEXT NOT NULL,
	success INTEGER NOT NULL,
	details TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log(at);
ALTER TABLE routes ADD COLUMN hub_visible INTEGER NOT NULL DEFAULT 1;
ALTER TABLE routes ADD COLUMN hub_title TEXT NOT NULL DEFAULT '';
ALTER TABLE routes ADD COLUMN hub_subtitle TEXT NOT NULL DEFAULT '';
CREATE TABLE route_icons (
	route_id     INTEGER PRIMARY KEY REFERENCES routes(id) ON DELETE CASCADE,
	content_type TEXT NOT NULL,
	data         BLOB NOT NULL,
	updated_at   TIMESTAMP NOT NULL
);
CREATE TABLE images (
	kind         TEXT NOT NULL,
	key          TEXT NOT NULL,
	content_type TEXT NOT NULL,
	data         BLOB NOT NULL,
	updated_at   TIMESTAMP NOT NULL,
	PRIMARY KEY (kind, key)
);
ALTER TABLE routes ADD COLUMN hub_icon_link TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN authelia_totp TEXT NOT NULL DEFAULT '';
