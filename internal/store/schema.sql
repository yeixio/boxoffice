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
	upgrade   INTEGER NOT NULL DEFAULT 1,
	locked    INTEGER NOT NULL DEFAULT 0 -- Do not touch: Box Office never changes these titles' files
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
	rating           DOUBLE PRECISION NOT NULL DEFAULT 0,
	monitored        INTEGER NOT NULL DEFAULT 1,
	profile_id       BIGINT NOT NULL REFERENCES quality_profiles(id),
	root_folder      TEXT NOT NULL,
	path             TEXT NOT NULL,
	added_at         BIGINT NOT NULL,
	refreshed_at     BIGINT NOT NULL DEFAULT 0,
	auto_upgrade     TEXT NOT NULL DEFAULT '' -- '' follows Settings; 'on' or 'off' for this movie
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
	rating       DOUBLE PRECISION NOT NULL DEFAULT 0,
	monitored    INTEGER NOT NULL DEFAULT 1,
	profile_id   BIGINT NOT NULL REFERENCES quality_profiles(id),
	root_folder  TEXT NOT NULL,
	path         TEXT NOT NULL,
	added_at     BIGINT NOT NULL,
	refreshed_at BIGINT NOT NULL DEFAULT 0,
	auto_upgrade TEXT NOT NULL DEFAULT '' -- '' follows Settings; 'on' or 'off' for this show
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

-- Where releases are searched for. Box Office's own search uses these once
-- it lands. Until then, imported ones wait here.
CREATE TABLE IF NOT EXISTS indexers (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	protocol       TEXT NOT NULL, -- usenet or torrent
	kind           TEXT NOT NULL, -- newznab, torznab, or the other app's name for it
	url            TEXT NOT NULL DEFAULT '',
	api_path       TEXT NOT NULL DEFAULT '',
	api_key        TEXT NOT NULL DEFAULT '',
	categories     TEXT NOT NULL DEFAULT '[]',
	enable_rss     INTEGER NOT NULL DEFAULT 1,
	enable_search  INTEGER NOT NULL DEFAULT 1,
	priority       INTEGER NOT NULL DEFAULT 25,
	settings       TEXT NOT NULL DEFAULT '{}', -- everything else, as the other app had it
	source         TEXT NOT NULL DEFAULT '',   -- radarr, sonarr… or '' when made here
	missing_secret INTEGER NOT NULL DEFAULT 0, -- a key or password couldn't be brought over
	UNIQUE (protocol, url, api_key)
);

-- Download clients other apps used (NZBGet, SABnzbd, qBittorrent…).
CREATE TABLE IF NOT EXISTS download_clients (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	kind           TEXT NOT NULL,
	protocol       TEXT NOT NULL,
	host           TEXT NOT NULL DEFAULT '',
	port           INTEGER NOT NULL DEFAULT 0,
	use_tls        INTEGER NOT NULL DEFAULT 0,
	url_base       TEXT NOT NULL DEFAULT '',
	username       TEXT NOT NULL DEFAULT '',
	password       TEXT NOT NULL DEFAULT '',
	category       TEXT NOT NULL DEFAULT '',
	priority       INTEGER NOT NULL DEFAULT 1,
	enabled        INTEGER NOT NULL DEFAULT 1,
	settings       TEXT NOT NULL DEFAULT '{}',
	source         TEXT NOT NULL DEFAULT '',
	missing_secret INTEGER NOT NULL DEFAULT 0,
	UNIQUE (kind, host, port, category)
);

-- Usenet servers for Box Office's own downloader.
CREATE TABLE IF NOT EXISTS usenet_servers (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	host        TEXT NOT NULL,
	port        INTEGER NOT NULL DEFAULT 563,
	use_tls     INTEGER NOT NULL DEFAULT 1,
	username    TEXT NOT NULL DEFAULT '',
	password    TEXT NOT NULL DEFAULT '',
	connections INTEGER NOT NULL DEFAULT 8,
	level       INTEGER NOT NULL DEFAULT 0, -- 0 first; higher levels are backups
	optional    INTEGER NOT NULL DEFAULT 0,
	retention   INTEGER NOT NULL DEFAULT 0, -- days; 0 unknown
	enabled     INTEGER NOT NULL DEFAULT 1,
	settings    TEXT NOT NULL DEFAULT '{}',
	source      TEXT NOT NULL DEFAULT '',
	UNIQUE (host, port, username)
);

-- Releases not to grab again.
CREATE TABLE IF NOT EXISTS blocklist (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	kind         TEXT NOT NULL, -- movie or series
	title_id     BIGINT NOT NULL,
	source_title TEXT NOT NULL,
	quality      INTEGER NOT NULL DEFAULT 0,
	protocol     TEXT NOT NULL DEFAULT '',
	indexer      TEXT NOT NULL DEFAULT '',
	reason       TEXT NOT NULL DEFAULT '',
	at           BIGINT NOT NULL,
	source       TEXT NOT NULL DEFAULT '',
	UNIQUE (kind, title_id, source_title)
);

-- Settings brought over from other apps that Box Office keeps as they
-- were: naming, tags, delay profiles, notifications, lists and the rest.
-- Features read them as they land.
CREATE TABLE IF NOT EXISTS imported_settings (
	app         TEXT NOT NULL,
	section     TEXT NOT NULL,
	data        TEXT NOT NULL,
	imported_at BIGINT NOT NULL,
	PRIMARY KEY (app, section)
);

-- Box Office's own downloads: the queue, and once finished, the history.
-- The NZB itself is kept in the downloads folder, by id.
CREATE TABLE IF NOT EXISTS downloads (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	name         TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'queued', -- queued, downloading, paused, verifying, repairing, unpacking, importing, completed, failed
	kind         TEXT NOT NULL DEFAULT '',       -- movie or series, when it's for a title
	title_id     BIGINT NOT NULL DEFAULT 0,
	episode_ids  TEXT NOT NULL DEFAULT '[]',
	quality      INTEGER NOT NULL DEFAULT 0,
	indexer      TEXT NOT NULL DEFAULT '',
	source       TEXT NOT NULL DEFAULT '',       -- search, upload…
	password     TEXT NOT NULL DEFAULT '',
	size         BIGINT NOT NULL DEFAULT 0,
	done_bytes   BIGINT NOT NULL DEFAULT 0,
	failed_bytes BIGINT NOT NULL DEFAULT 0,
	done_files   TEXT NOT NULL DEFAULT '[]',     -- files of the NZB already fetched, by index
	position     INTEGER NOT NULL DEFAULT 0,
	folder       TEXT NOT NULL DEFAULT '',
	message      TEXT NOT NULL DEFAULT '',
	imported_to  TEXT NOT NULL DEFAULT '',
	quarantined  TEXT NOT NULL DEFAULT '',       -- where its files went when they looked unsafe
	added_at     BIGINT NOT NULL,
	started_at   BIGINT NOT NULL DEFAULT 0,
	finished_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS downloads_status ON downloads (status, position);

-- What Box Office tells people about: a download starting, a title arriving
-- in the library, a download that failed or looked unsafe. Matinee reads
-- them (GET /radarr/api/v3/boxoffice/notifications) and passes them on to
-- the people who asked for the title, and to admins. Data is the
-- notification as JSON; only the last few weeks are kept.
CREATE TABLE IF NOT EXISTS notifications (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	at   BIGINT NOT NULL,
	data TEXT NOT NULL
);

-- When Box Office last searched the indexers for a movie or an episode, so
-- automatic searches for missing titles and upgrades take turns, and how
-- many searches in a row found nothing it could take, so those wait longer.
CREATE TABLE IF NOT EXISTS searched (
	kind   TEXT NOT NULL, -- movie or episode
	id     BIGINT NOT NULL,
	at     BIGINT NOT NULL,
	misses INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (kind, id)
);

-- How each indexer is doing: errors in a row and how long it rests after
-- them, the limits it reports for its API, and where RSS sync got to.
CREATE TABLE IF NOT EXISTS indexer_status (
	indexer_id    BIGINT PRIMARY KEY,
	failures      INTEGER NOT NULL DEFAULT 0,
	resting_until BIGINT NOT NULL DEFAULT 0,
	last_error    TEXT NOT NULL DEFAULT '',
	failed_at     BIGINT NOT NULL DEFAULT 0,
	api_max       INTEGER NOT NULL DEFAULT 0, -- as the indexer reports it, 0 if it doesn't
	grab_max      INTEGER NOT NULL DEFAULT 0,
	rss_guid      TEXT NOT NULL DEFAULT '',   -- the newest release RSS sync has seen
	rss_at        BIGINT NOT NULL DEFAULT 0,  -- and when it was posted
	rss_synced_at BIGINT NOT NULL DEFAULT 0
);

-- Each indexer's API calls and NZB downloads, by UTC day, to stay inside
-- its daily limits. auto counts the searches Box Office made by itself.
CREATE TABLE IF NOT EXISTS indexer_usage (
	indexer_id BIGINT NOT NULL,
	day        TEXT NOT NULL,
	queries    INTEGER NOT NULL DEFAULT 0,
	grabs      INTEGER NOT NULL DEFAULT 0,
	auto       INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (indexer_id, day)
);

-- Upgrades Box Office grabbed by itself, by local day, for the monthly
-- limit on automatic upgrades.
CREATE TABLE IF NOT EXISTS auto_upgrades (
	day TEXT NOT NULL PRIMARY KEY,
	n   INTEGER NOT NULL DEFAULT 0
);

-- Bytes downloaded from each news server, by local day, for the monthly
-- data cap and block accounts.
CREATE TABLE IF NOT EXISTS data_usage (
	day       TEXT NOT NULL,
	server_id BIGINT NOT NULL,
	bytes     BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (day, server_id)
);

-- Music, once it's turned on in Settings: artists from MusicBrainz and
-- their albums (MusicBrainz release groups).
CREATE TABLE IF NOT EXISTS artists (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	mbid           TEXT NOT NULL UNIQUE,
	name           TEXT NOT NULL,
	sort_name      TEXT NOT NULL,
	type           TEXT NOT NULL DEFAULT '',
	country        TEXT NOT NULL DEFAULT '',
	disambiguation TEXT NOT NULL DEFAULT '',
	monitored      INTEGER NOT NULL DEFAULT 1,
	-- Which kinds of album new ones are watched for: JSON list such as
	-- ["Album"], ["Album","EP"]; "Album" means studio albums only.
	album_types    TEXT NOT NULL DEFAULT '["Album"]',
	root_folder    TEXT NOT NULL,
	path           TEXT NOT NULL,
	added_at       BIGINT NOT NULL,
	refreshed_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS albums (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	artist_id       BIGINT NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
	mbid            TEXT NOT NULL UNIQUE,
	title           TEXT NOT NULL,
	type            TEXT NOT NULL DEFAULT '',
	secondary_types TEXT NOT NULL DEFAULT '[]', -- JSON list
	released        TEXT NOT NULL DEFAULT '',   -- YYYY, YYYY-MM or YYYY-MM-DD
	monitored       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS albums_artist ON albums (artist_id);

-- Books, for ebooks and audiobooks alike, once either is turned on in
-- Settings: authors from Open Library and their books (Open Library
-- works). Each book can be wanted as an ebook, an audiobook, or both.
CREATE TABLE IF NOT EXISTS authors (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	ol_key         TEXT NOT NULL UNIQUE,
	name           TEXT NOT NULL,
	sort_name      TEXT NOT NULL,
	born           TEXT NOT NULL DEFAULT '',
	died           TEXT NOT NULL DEFAULT '',
	bio            TEXT NOT NULL DEFAULT '',
	-- Whether books that come out from now on are wanted in each format.
	new_ebooks     INTEGER NOT NULL DEFAULT 0,
	new_audiobooks INTEGER NOT NULL DEFAULT 0,
	added_at       BIGINT NOT NULL,
	refreshed_at   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS books (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	author_id      BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
	ol_key         TEXT NOT NULL UNIQUE,
	title          TEXT NOT NULL,
	subtitle       TEXT NOT NULL DEFAULT '',
	year           INTEGER NOT NULL DEFAULT 0,
	cover_id       BIGINT NOT NULL DEFAULT 0,
	editions       INTEGER NOT NULL DEFAULT 0,
	want_ebook     INTEGER NOT NULL DEFAULT 0,
	want_audiobook INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS books_author ON books (author_id);
