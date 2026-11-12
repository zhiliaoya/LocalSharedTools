-- LocalShT V3 schema (SQLite)
-- 九表：role_types / share_types / share_levels / file_types / tokens / users / collections / records / shares
-- 附：audit_log（M5 可选审计）

CREATE TABLE IF NOT EXISTS role_types (
  id          INTEGER PRIMARY KEY CHECK (id BETWEEN 0 AND 6),
  name        TEXT    NOT NULL UNIQUE,
  perm_mask   INTEGER NOT NULL CHECK (perm_mask BETWEEN 0 AND 63),
  description TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS share_types (
  id          INTEGER PRIMARY KEY CHECK (id IN (0, 1)),
  name        TEXT    NOT NULL UNIQUE,
  description TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS share_levels (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL UNIQUE,
  perm_mask   INTEGER NOT NULL CHECK (perm_mask BETWEEN 0 AND 63),
  description TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS file_types (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL UNIQUE,
  description TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS users (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL DEFAULT '',
  role_type_id INTEGER NOT NULL REFERENCES role_types(id),
  client_id    TEXT    NOT NULL DEFAULT '',
  active       INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT    NOT NULL,
  updated_at   TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
  token      TEXT    PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT    NOT NULL,
  revoked_at TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS collections (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  name       TEXT    NOT NULL DEFAULT '默认集合',
  is_default INTEGER NOT NULL DEFAULT 1,
  created_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS records (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id           INTEGER NOT NULL REFERENCES users(id),
  username          TEXT    NOT NULL DEFAULT '',
  file_type_id      INTEGER NOT NULL DEFAULT 0 REFERENCES file_types(id),
  content           TEXT    NOT NULL DEFAULT '',
  filesize          INTEGER NOT NULL DEFAULT 0,
  ext               TEXT    NOT NULL DEFAULT '',
  collection_id     INTEGER NOT NULL REFERENCES collections(id),
  created_at        TEXT    NOT NULL,
  ts                INTEGER NOT NULL,
  legacy_message_id INTEGER
);

CREATE TABLE IF NOT EXISTS shares (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  sharer_user_id  INTEGER NOT NULL REFERENCES users(id),
  target_token    TEXT    NOT NULL REFERENCES tokens(token),
  share_type_id   INTEGER NOT NULL REFERENCES share_types(id),
  share_level_id  INTEGER NOT NULL REFERENCES share_levels(id),
  active          INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT    NOT NULL,
  revoked_at      TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS audit_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  at            TEXT    NOT NULL,
  actor_user_id INTEGER NOT NULL DEFAULT 0,
  actor_role_id INTEGER NOT NULL DEFAULT 0,
  action        TEXT    NOT NULL,
  detail        TEXT    NOT NULL DEFAULT '',
  ip            TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_client_id
  ON users(client_id) WHERE client_id <> '';
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role_type_id);

CREATE INDEX IF NOT EXISTS idx_tokens_user_active ON tokens(user_id, active);

CREATE UNIQUE INDEX IF NOT EXISTS idx_collections_default
  ON collections(user_id) WHERE is_default = 1;

CREATE INDEX IF NOT EXISTS idx_records_ts ON records(ts);
CREATE INDEX IF NOT EXISTS idx_records_user_ts ON records(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_records_collection ON records(collection_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_records_legacy
  ON records(legacy_message_id) WHERE legacy_message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_shares_target_active
  ON shares(target_token, active);
CREATE INDEX IF NOT EXISTS idx_shares_sharer_active
  ON shares(sharer_user_id, active);
