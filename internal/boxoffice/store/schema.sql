-- Box Office's schema, written for SQLite. For PostgreSQL, "INTEGER PRIMARY
-- KEY AUTOINCREMENT" becomes "BIGSERIAL PRIMARY KEY" and BLOB becomes BYTEA;
-- nothing else differs. Times are Unix seconds.

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS quality_profiles (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	name      TEXT NOT NULL UNIQUE,
	allowed   TEXT NOT NULL, -- JSON list of quality ids
	cutoff    INTEGER NOT NULL,
	upgrade   INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS root_folders (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL, -- movie or series
	path TEXT NOT NULL,
	UNIQUE (kind, path)
);

CREATE TABLE IF NOT EXISTS movies (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	tmdb_id          BIGINT NOT NULL UNIQUE,
	imdb_id          TEXT NOT NULL DEFAULT '',
	title            TEXT NOT NULL,
	sort_title       TEXT NOT NULL,
	year             INTEGER NOT NULL DEFAULT 0,
	overview         TEXT NOT NULL DEFAULT '',
	poster           TEXT NOT NULL DEFAULT '',
	backdrop         TEXT NOT NULL DEFAULT '',
	runtime          INTEGER NOT NULL DEFAULT 0,
	status           TEXT NOT NULL DEFAULT '',
	in_cinemas       BIGINT,
	digital_release  BIGINT,
	physical_release BIGINT,
	monitored        INTEGER NOT NULL DEFAULT 1,
	profile_id       BIGINT NOT NULL REFERENCES quality_profiles(id),
	root_folder      TEXT NOT NULL,
	path             TEXT NOT NULL,
	added_at         BIGINT NOT NULL,
	refreshed_at     BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS series (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	tmdb_id      BIGINT NOT NULL UNIQUE,
	tvdb_id      BIGINT NOT NULL DEFAULT 0,
	imdb_id      TEXT NOT NULL DEFAULT '',
	title        TEXT NOT NULL,
	sort_title   TEXT NOT NULL,
	year         INTEGER NOT NULL DEFAULT 0,
	overview     TEXT NOT NULL DEFAULT '',
	poster       TEXT NOT NULL DEFAULT '',
	backdrop     TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL DEFAULT '',
	network      TEXT NOT NULL DEFAULT '',
	runtime      INTEGER NOT NULL DEFAULT 0,
	monitored    INTEGER NOT NULL DEFAULT 1,
	profile_id   BIGINT NOT NULL REFERENCES quality_profiles(id),
	root_folder  TEXT NOT NULL,
	path         TEXT NOT NULL,
	added_at     BIGINT NOT NULL,
	refreshed_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS series_tvdb ON series (tvdb_id);

CREATE TABLE IF NOT EXISTS seasons (
	series_id BIGINT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
	number    INTEGER NOT NULL,
	monitored INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (series_id, number)
);

CREATE TABLE IF NOT EXISTS episodes (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	series_id BIGINT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
	season    INTEGER NOT NULL,
	number    INTEGER NOT NULL,
	title     TEXT NOT NULL DEFAULT '',
	overview  TEXT NOT NULL DEFAULT '',
	air_date  BIGINT,
	monitored INTEGER NOT NULL DEFAULT 1,
	UNIQUE (series_id, season, number)
);
CREATE INDEX IF NOT EXISTS episodes_air ON episodes (air_date);

-- A file on disk for a movie or an episode.
CREATE TABLE IF NOT EXISTS media_files (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	kind       TEXT NOT NULL, -- movie or episode
	owner_id   BIGINT NOT NULL,
	path       TEXT NOT NULL UNIQUE,
	size       BIGINT NOT NULL DEFAULT 0,
	quality    INTEGER NOT NULL DEFAULT 0,
	added_at   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS media_files_owner ON media_files (kind, owner_id);

-- Background work, and the record of it that Activity shows.
CREATE TABLE IF NOT EXISTS jobs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	kind        TEXT NOT NULL,
	subject     TEXT NOT NULL DEFAULT '', -- what it's about, for people
	payload     TEXT NOT NULL DEFAULT '{}',
	status      TEXT NOT NULL DEFAULT 'queued', -- queued, running, done, skipped, failed
	message     TEXT NOT NULL DEFAULT '',
	created_at  BIGINT NOT NULL,
	started_at  BIGINT NOT NULL DEFAULT 0,
	finished_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs (status, id);

-- Ids another app used for a row Box Office imported from it, so clients of
-- that app's API (such as Matinee's Radarr and Sonarr settings) keep working.
CREATE TABLE IF NOT EXISTS legacy_ids (
	app       TEXT NOT NULL, -- radarr or sonarr
	kind      TEXT NOT NULL, -- profile
	legacy_id BIGINT NOT NULL,
	local_id  BIGINT NOT NULL,
	PRIMARY KEY (app, kind, legacy_id)
);

-- Imports from other apps, and what each one did.
CREATE TABLE IF NOT EXISTS migrations (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	app         TEXT NOT NULL,
	url         TEXT NOT NULL,
	status      TEXT NOT NULL, -- running, done, failed
	report      TEXT NOT NULL DEFAULT '{}',
	created_at  BIGINT NOT NULL,
	finished_at BIGINT NOT NULL DEFAULT 0
);
