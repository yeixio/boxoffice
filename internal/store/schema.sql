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
	created_at    BIGINT NOT NULL,
	language      TEXT NOT NULL DEFAULT '' -- '' follows the browser
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
	kind       TEXT NOT NULL, -- movie, episode, ebook or audiobook (an audiobook's path is its folder)
	owner_id   BIGINT NOT NULL,
	path       TEXT NOT NULL UNIQUE,
	size       BIGINT NOT NULL DEFAULT 0,
	quality    INTEGER NOT NULL DEFAULT 0,
	added_at   BIGINT NOT NULL,
	-- What the quality check found inside a movie or episode (JSON
	-- quality.Media), when, and the file's size then; guessed is the
	-- quality it had before the check corrected it, -1 when it didn't.
	media       TEXT NOT NULL DEFAULT '',
	probed_at   BIGINT NOT NULL DEFAULT 0,
	probed_size BIGINT NOT NULL DEFAULT 0,
	guessed     INTEGER NOT NULL DEFAULT -1
);
CREATE INDEX IF NOT EXISTS media_files_owner ON media_files (kind, owner_id);

-- What people asked for beyond the profile: the edition a movie should be
-- (Director's Cut, say), and a better or another copy wanted now, from
-- Matinee's "Get another edition" and "Ask for a better copy".
CREATE TABLE IF NOT EXISTS title_wants (
	kind       TEXT NOT NULL,            -- movie or episode
	owner_id   BIGINT NOT NULL,
	edition    TEXT NOT NULL DEFAULT '', -- the edition its copy should be
	want       TEXT NOT NULL DEFAULT '', -- better, replace (the copy's bad) or ''
	note       TEXT NOT NULL DEFAULT '',
	updated_at BIGINT NOT NULL,
	problem    TEXT NOT NULL DEFAULT '', -- what someone reported in Matinee: wrong, language, video, audio
	language   TEXT NOT NULL DEFAULT '', -- the language its sound should be in (ISO 639-1), for language
	reported_by TEXT NOT NULL DEFAULT '', -- who reported it, by their Matinee name
	PRIMARY KEY (kind, owner_id)
);

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
	locked         INTEGER NOT NULL DEFAULT 0, -- Do not touch: Box Office never searches for or changes these books' files
	added_at       BIGINT NOT NULL,
	refreshed_at   BIGINT NOT NULL DEFAULT 0,
	photo          TEXT NOT NULL DEFAULT '',   -- a picture of the author, from Open Library or Wikimedia Commons
	wikidata       TEXT NOT NULL DEFAULT '',   -- the author's Wikidata item, Q892
	details_at     BIGINT NOT NULL DEFAULT 0   -- when the photo and series were last looked up
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
	want_audiobook INTEGER NOT NULL DEFAULT 0,
	series         TEXT NOT NULL DEFAULT '',   -- the series or collection it's part of, The Lord of the Rings
	series_pos     TEXT NOT NULL DEFAULT ''    -- its place in it: 1, 2, 1-3 for an omnibus, or blank
);
CREATE INDEX IF NOT EXISTS books_author ON books (author_id);

-- Games, once they're turned on in Settings: each system's folder found in
-- a games root folder, and the games in it, checked against MAME's list or
-- No-Intro's. Box Office only reads these files: it never changes, moves
-- or deletes them.
CREATE TABLE IF NOT EXISTS game_systems (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	system      TEXT NOT NULL,              -- mame, snes, ...
	path        TEXT NOT NULL UNIQUE,
	root_folder TEXT NOT NULL,
	locked      INTEGER NOT NULL DEFAULT 0, -- Do not touch: not looked at again until it's turned off
	format      TEXT NOT NULL DEFAULT '',   -- MAME: non-merged, split or merged
	list        TEXT NOT NULL DEFAULT '',   -- the list games were checked against: MAME 0.289, or No-Intro's date
	support     INTEGER NOT NULL DEFAULT 0, -- MAME: BIOS and device sets, which aren't games
	note        TEXT NOT NULL DEFAULT '',   -- why the last check couldn't finish
	added_at    BIGINT NOT NULL,
	checked_at  BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS games (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	system_id    BIGINT NOT NULL REFERENCES game_systems(id) ON DELETE CASCADE,
	name         TEXT NOT NULL,              -- MAME's short name (pacman), or the name in No-Intro's list
	title        TEXT NOT NULL,
	parent       TEXT NOT NULL DEFAULT '',   -- what it's grouped under: MAME's parent set, or the console game's title
	parent_title TEXT NOT NULL DEFAULT '',
	year         TEXT NOT NULL DEFAULT '',
	maker        TEXT NOT NULL DEFAULT '',
	players      INTEGER NOT NULL DEFAULT 0,
	genre        TEXT NOT NULL DEFAULT '',
	path         TEXT NOT NULL,              -- its file: for a clone in a merged set, its parent's
	in_parent    INTEGER NOT NULL DEFAULT 0,
	size         BIGINT NOT NULL DEFAULT 0,
	mtime        BIGINT NOT NULL DEFAULT 0,
	crc          TEXT NOT NULL DEFAULT '',   -- consoles: the CRC that matched, so an unchanged file isn't read again
	serial       TEXT NOT NULL DEFAULT '',   -- discs and downloads: the serial or title ID read from the game
	status       TEXT NOT NULL DEFAULT '',   -- complete, identified, missing, wrong, bios, unknown or unreadable
	problems     TEXT NOT NULL DEFAULT '[]', -- JSON list of {kind, name}
	art          TEXT NOT NULL DEFAULT '',   -- the name libretro's thumbnails have it under
	added_at     BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS games_system ON games (system_id);

-- Games wanted: picked from a system's list (No-Intro's or MAME's) to be
-- searched for and downloaded. A want is forgotten once a check finds the
-- game in its system's folder.
CREATE TABLE IF NOT EXISTS game_wants (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	system   TEXT NOT NULL,              -- mame, snes, ...
	name     TEXT NOT NULL,              -- the version wanted: its name in No-Intro's list, or MAME's set name
	title    TEXT NOT NULL,
	grp      TEXT NOT NULL DEFAULT '',   -- consoles: the title's letters and digits, so any version of it counts
	year     TEXT NOT NULL DEFAULT '',
	maker    TEXT NOT NULL DEFAULT '',
	crc      TEXT NOT NULL DEFAULT '',   -- consoles: the version's CRC
	serial   TEXT NOT NULL DEFAULT '',   -- discs and downloads: the version's serial or title ID
	size     BIGINT NOT NULL DEFAULT 0,  -- consoles: the version's file size
	art      TEXT NOT NULL DEFAULT '',   -- the name libretro's thumbnails have it under
	got_at   BIGINT NOT NULL DEFAULT 0,  -- when a download of it was moved in, before a check has seen it
	added_at BIGINT NOT NULL,
	UNIQUE (system, name)
);

-- The request site: the people who use it, what they ask for, and the
-- problems they report. Members are not admins: they sign in to the
-- request site only, never to the admin UI, and have their own sessions.
CREATE TABLE IF NOT EXISTS members (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE, -- lowercased
	display_name  TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL DEFAULT '', -- empty for Matinee accounts
	source        TEXT NOT NULL DEFAULT 'local', -- local or matinee
	auto_approve  TEXT NOT NULL DEFAULT '', -- '' follows Settings, or on or off
	quota_movies  INTEGER NOT NULL DEFAULT -1, -- a week; -1 follows Settings, 0 is no limit
	quota_shows   INTEGER NOT NULL DEFAULT -1,
	disabled      INTEGER NOT NULL DEFAULT 0,
	created_at    BIGINT NOT NULL,
	seen_at       BIGINT NOT NULL DEFAULT 0,
	language      TEXT NOT NULL DEFAULT '' -- '' follows the browser
);

CREATE TABLE IF NOT EXISTS member_sessions (
	token_hash TEXT PRIMARY KEY,
	member_id  BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
	expires_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS requests (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	member_id  BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL, -- movie or series
	tmdb_id    BIGINT NOT NULL,
	title      TEXT NOT NULL,
	year       INTEGER NOT NULL DEFAULT 0,
	poster     TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'pending', -- pending, approved, declined
	note       TEXT NOT NULL DEFAULT '', -- from the person asking
	reply      TEXT NOT NULL DEFAULT '', -- from the admin, such as why it was declined
	created_at BIGINT NOT NULL,
	decided_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS requests_member ON requests (member_id, created_at);
CREATE INDEX IF NOT EXISTS requests_title ON requests (kind, tmdb_id);

CREATE TABLE IF NOT EXISTS issues (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	member_id   BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL, -- movie or series
	tmdb_id     BIGINT NOT NULL,
	title       TEXT NOT NULL,
	year        INTEGER NOT NULL DEFAULT 0,
	poster      TEXT NOT NULL DEFAULT '',
	season      INTEGER NOT NULL DEFAULT 0, -- 0 for a movie or the whole show
	episode     INTEGER NOT NULL DEFAULT 0,
	problem     TEXT NOT NULL, -- wont_play, wrong_title, picture, sound, subtitles, other
	message     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'open', -- open or resolved
	reply       TEXT NOT NULL DEFAULT '',
	created_at  BIGINT NOT NULL,
	resolved_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS issues_status ON issues (status, created_at);

-- Ebooks sent to e-readers by email (Settings → Send to e-readers), so a
-- book goes to each reader once and the admin can see what went where.
CREATE TABLE IF NOT EXISTS ereader_sends (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id   TEXT NOT NULL,
	device_name TEXT NOT NULL DEFAULT '',
	book_id     BIGINT NOT NULL DEFAULT 0, -- 0 for a test
	title       TEXT NOT NULL DEFAULT '',
	auto        INTEGER NOT NULL DEFAULT 0, -- sent on its own after a download
	error       TEXT NOT NULL DEFAULT '', -- why it couldn't go, if it didn't
	sent_at     BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS ereader_sends_book ON ereader_sends (device_id, book_id);

-- Problem files: what a fix on the Problem files page did, so it can be
-- put back. A fix that sets originals aside waits for the admin to delete
-- them for good or put them back; nothing it set aside is ever deleted
-- without that.
CREATE TABLE IF NOT EXISTS problem_fixes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	action     TEXT NOT NULL, -- rename, set_aside, keep_one, convert, join, record, forget
	kind       TEXT NOT NULL DEFAULT '', -- the kind of problem it fixed: not_listed, leftover, duplicate…
	detail     TEXT NOT NULL DEFAULT '', -- what more the problem said, such as second_folder
	title      TEXT NOT NULL DEFAULT '', -- what it was about, for the list
	root       TEXT NOT NULL,
	state      TEXT NOT NULL DEFAULT 'waiting', -- waiting, done (originals deleted), undone (put back)
	created_at BIGINT NOT NULL,
	decided_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS problem_fixes_state ON problem_fixes (state, created_at);

CREATE TABLE IF NOT EXISTS problem_fix_files (
	fix_id BIGINT NOT NULL REFERENCES problem_fixes(id) ON DELETE CASCADE,
	role   TEXT NOT NULL, -- aside (an original, now in .recycle), moved (renamed or moved), made (a new file a fix wrote)
	src    TEXT NOT NULL, -- where it was
	dest   TEXT NOT NULL, -- where it is now
	size   BIGINT NOT NULL DEFAULT 0,
	-- The record it was, so putting it back restores it: media_files' kind and owner, or '' and 0.
	file_kind  TEXT NOT NULL DEFAULT '',
	file_owner BIGINT NOT NULL DEFAULT 0,
	quality    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS problem_fix_files_fix ON problem_fix_files (fix_id);
