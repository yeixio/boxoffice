# Settings inventory

Every setting in Box Office, where it lives, and how it's classified, as asked by section 10 of the [Navigation & Settings Design Principles](navigation-and-settings.md). Keep it up to date: a pull request that adds, moves or removes a setting changes its row here.

**Columns**

- **Where**: the page, tab and card a person changes it on. Settings cards have an id, which is also their `/settings#<id>` link.
- **Scope**: *Account* (one person), *Application* (all of Box Office), or *Resource* (one title, folder, profile, server, indexer, list, rule or device).
- **Level**: *Basic*, *Advanced*, *Developer* or *Admin*, as in section 4.2.
- **Default**: on a new install.
- **Impact**: *Behaviour*, *Visual*, *Security* or *Data* (can lose or change files or records).

Settings only an environment variable can set (`BOXOFFICE_*`, see the README's Configuration table) are listed at the end.

## Your account

Personal: each admin has their own.

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Language | The language Box Office shows this person, on every device they sign in on | Settings → Your account → `#language` | Account | Basic | Automatic (the browser's) | One of 32 catalogs | Visual |
| Password | This person's password | Settings → Your account → `#password` | Account | Basic | Set at first run | At least 8 characters; signs every device out | Security |
| Passkeys | Fingerprint, face, screen lock or security key sign-in | Settings → Your account → `#passkeys` | Account | Basic | None | Works only at the address it was made at | Security |
| One-time code | A 6-digit authenticator code after the password, with recovery codes | Settings → Your account → `#code` | Account | Advanced | Off | Not asked after a passkey or single sign-on | Security |

## General

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Time zone | The clock for the speed schedule, data cap month, daily limits and times in messages | Settings → General → `#time-zone` | Application | Basic | The server's own (`TZ`), else UTC | — | Behaviour |
| Kinds of media | Music, audiobooks, ebooks, comics, games, home videos and photos, each on or off | Settings → General → `#media-types` | Application | Basic | All off | Each needs a root folder | Behaviour, Visual (adds sidebar entries) |

## Library

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Root folders | Where each kind of media lives on disk; Scan adds what's already there | Settings → Library → `#folders-movie`, `#folders-series`, `#folders-<kind>` | Resource | Basic | None | Must be readable and writable | Data |
| Move titles: speed limit | How fast titles are moved to another root folder | Settings → Library → `#moves` | Application | Advanced | 0 (no limit) | — | Behaviour |
| New shows: which episodes | What a newly added show watches | Settings → Library → `#new-shows` | Application | Basic | The last 3 seasons and new ones | — | Behaviour |
| New shows: how many seasons | Seasons counted for the setting above | Settings → Library → `#new-shows` | Application | Basic | 3 | Only for "the last N seasons" | Behaviour |
| Rules for new titles | Folder and quality profile by genre or language for new titles | Settings → Library → `#add-rules` | Application (each rule a Resource) | Advanced | None | Root folders, profiles | Behaviour |
| Lists | Followed TMDB, Trakt and Plex lists; what's added from them | Settings → Library → `#lists` | Resource | Advanced | None; checked every 6 hours, 20 titles a check | Trakt needs a client ID | Behaviour |
| Subtitles: fetch | Fetch missing subtitles | Settings → Library → `#subtitles` | Application | Basic | Off | ffprobe | Behaviour |
| Subtitles: languages | Which languages to fetch | Settings → Library → `#subtitles` | Application | Basic | None | — | Behaviour |
| Subtitles: prefer SDH | Prefer subtitles for the deaf and hard of hearing | Settings → Library → `#subtitles` | Application | Advanced | Off | — | Behaviour |
| Subtitles: files a day | How many files are looked up a day | Settings → Library → `#subtitles` | Application | Advanced | 300 | — | Behaviour |
| Subtitles: sources | Podnapisi on/off; OpenSubtitles API key, username, password | Settings → Library → `#subtitles` | Application | Advanced | Podnapisi on, OpenSubtitles off | OpenSubtitles needs a key | Behaviour, Security (stores a password) |
| Drop unwanted tracks | Remove audio and subtitle tracks in other languages as files come in | Settings → Library → `#tracks` | Application | Advanced | Off | Languages to keep; MKVToolNix | Data |
| Clean-up rules | Pick out watched or unwatched titles to delete | Settings → Library → `#cleanup` | Resource | Advanced | None | Matinee watch history | Data |
| Clean-up: deletions a day | Most titles a rule deletes by itself in a day | Settings → Library → `#cleanup` | Application | Advanced | 5 | Rules set to delete by themselves | Data |
| Recycle folders: delete replaced copies | When copies set aside in `.recycle` are deleted for good | Settings → Library → `#recycle` | Application | Basic | After 7 days | — | Data |

Quality check (`#quality-check`), Problem files (`#problems`), the titles file (`#titles-file`) and indexer statistics (`#indexer-stats`) are actions or reports, not settings.

## Downloads

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Built-in downloader on | Download with Box Office's own Usenet downloader | Settings → Downloads → `#downloads` | Application | Basic | Off | A news server | Behaviour |
| Paused | Stop downloading for now | Downloads page; Settings → Downloads | Application | Basic | Off | — | Behaviour |
| Speed limit | Highest download speed | Settings → Downloads → `#downloads` | Application | Basic | 0 (no limit) | Overridden by the speed schedule | Behaviour |
| Import when done | Move finished downloads into the library | Settings → Downloads → `#downloads` | Application | Basic | Off | Root folders | Data |
| Delete failed downloads | Delete a failed download's files | Settings → Downloads → `#downloads` | Application | Advanced | On | — | Data |
| Quarantine days | Keep downloads with suspicious files aside this many days | Settings → Downloads → `#downloads` | Application | Advanced | 0 | — | Data |
| Keep free (GB) | Downloads wait while the disk has less than this free | Settings → Downloads → `#downloads` | Application | Advanced | 5 GB | — | Behaviour |
| Speed schedule | Full speed, a limit or a pause by time of day | Settings → Downloads → `#speed-schedule` | Application | Advanced | Off | Time zone | Behaviour |
| Download order | Order of grab reasons; new-title window; jump the queue | Settings → Downloads → `#download-order` | Application | Advanced | New episodes, new movies, backlog…, upgrades last; 30 days; no jumping | — | Behaviour |
| News servers | Usenet servers, connections, block/backup use | Settings → Downloads → `#news-servers` | Resource | Basic | None | — | Security (stores a password) |
| Download apps | SABnzbd, NZBGet, qBittorrent and the like | Settings → Downloads → `#download-clients` | Resource | Basic | None | — | Security (stores a password or key) |

## Searching

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Indexers | Where releases are searched, each with a daily budget | Settings → Searching → `#indexers` | Resource | Basic | None | — | Security (stores an API key) |
| Quality profiles | Qualities wanted, good enough, upgrade until, sizes, release preferences | Settings → Searching → `#profiles` | Resource | Basic | Ready-made profiles | — | Behaviour |
| Automatic searches on | Search for missing titles by itself | Settings → Searching → `#automatic-searches` | Application | Basic | On | Indexers, a way to download | Behaviour |
| Searches a day | Daily search budget | Settings → Searching → `#automatic-searches` | Application | Advanced | 100 | — | Behaviour |
| RSS sync | Watch indexers' new releases, and how often | Settings → Searching → `#automatic-searches` | Application | Advanced | On, every 15 minutes | — | Behaviour |
| Upgrades | Search for better copies, and how many a month | Settings → Searching → `#automatic-searches` | Application | Advanced | Off, 0 a month | Profiles' upgrade until | Behaviour, Data |
| Replace at the same quality | A replacement for a bad copy keeps its quality | Settings → Searching → `#automatic-searches` | Application | Advanced | On | — | Behaviour |
| PROPER and REPACK | Fixed releases replace flawed ones | Settings → Searching → `#automatic-searches` | Application | Advanced | On | RSS sync | Behaviour, Data |
| Wait before grabbing | Hold new releases so a better one can turn up | Settings → Searching → `#automatic-searches` | Application | Advanced | 30 minutes | — | Behaviour |
| Replacements a day | Most bad copies replaced in a day | Settings → Searching → `#automatic-searches` | Application | Advanced | 10 | — | Behaviour |
| Search order | Missing first, by download order | Settings → Searching → `#automatic-searches` | Application | Advanced | By download order | Download order | Behaviour |
| Early grabs | Take a release before its date when it meets the cutoff | Settings → Searching → `#automatic-searches` | Application | Advanced | At the cutoff | Profiles | Behaviour |
| Data cap | Monthly cap, when upgrades and then missing titles stop, reset day | Settings → Searching → `#data-cap` | Application | Advanced | On, 3000 GB, 70 % and 90 %, day 1 | Time zone | Behaviour |

## Notifications

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Alert rules | Disk almost full (90 %), root folder unreadable, errors pile up (10 in 15 min), downloads keep failing (3 in 60 min), starts or restarts, backups, problems | Settings → Notifications → `#alerts` | Application | Basic | All on | — | Behaviour |
| Alert email | Mail server, sender and recipients | Settings → Notifications → `#alerts` | Application | Advanced | Off | — | Security (stores a password) |
| Webhooks | Discord, ntfy, Home Assistant or any address | Settings → Notifications → `#alerts` | Resource | Advanced | None | — | Behaviour |
| Prometheus metrics | `/metrics` with a token | Settings → Notifications → `#alerts` | Application | Developer | Off | — | Security |
| Phone notifications | Push to this phone or computer, by topic (ready, requests, problems) | Settings → Notifications → `#phone` | Resource (a device) | Basic | Off | Browser permission; HTTPS | Behaviour |

## Connections

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TMDB key | Key for looking titles up | Settings → Connections → `#tmdb` | Application | Basic | Built in, or `BOXOFFICE_TMDB_KEY` | — | Behaviour |
| API key | The key Matinee and other apps use | Settings → Connections → `#matinee` | Application | Developer | Made at first run, or `BOXOFFICE_API_KEY` | Matinee needs the new one after a change | Security |
| E-readers | Kindle, PocketBook and other email-in readers; send by itself | Settings → Connections → `#ereaders` | Resource | Basic | None | A mail server (alerts' or its own) | Behaviour |
| E-reader mail server | Its own server, or the alerts' | Settings → Connections → `#ereaders` | Application | Advanced | Same as alerts | — | Security (stores a password) |
| Move in | Bring titles and settings over from Radarr, Sonarr, Lidarr, Readarr, NZBGet | Move in page (until moved in); Settings → Connections → `#move-in` | Application | Basic | — | — | Data |
| Calendar feed | Private iCal address for Coming up | Coming up | Application | Advanced | A random token | — | Security |

## System

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Single sign-on | OpenID Connect provider, client, admins, admin group, members and member group, sign-in button name, request site use | Settings → System → `#sso` | Application | Admin | Off | A provider | Security |
| Backups | How often, how many to keep, also send to (S3 and the like), passphrase | Settings → System → `#backups` | Application | Admin | Every 24 hours, keep 14, nowhere else, no passphrase | — | Data, Security |
| Restart | Stop cleanly and start again | Settings → System → `#restart` | Application | Admin | — | — | Behaviour |
| Database | SQLite or PostgreSQL; move to another | Settings → System → `#database` | Application | Admin | SQLite in the data folder | An empty PostgreSQL database | Data |
| Debug logging | More detail in the log | Health → Log | Application | Developer | Off (`BOXOFFICE_DEBUG`) | — | Behaviour |

## Requests (request site)

On the Requests page, which is where requests are handled (section 3.4).

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Request site on | Members can ask for titles | Requests → settings | Application | Basic | On | — | Behaviour |
| Name, welcome, colour, logo, address | How the site looks and where it answers | Requests → settings | Application | Basic | "Movie Night", a welcome line, Box Office's colours | Its own host name for its own address | Visual |
| Sign-in ways | Box Office accounts, Matinee, single sign-on; sign-up | Requests → settings | Application | Admin | Local sign-in on, sign-up off | Matinee address; single sign-on | Security |
| Approval and limits | Manual or automatic approval; movies and shows a week | Requests → settings | Application | Basic | Manual, no limits | — | Behaviour |
| Profiles for requests | Quality profile for requested movies and shows | Requests → settings | Application | Advanced | Each kind's default | Profiles | Behaviour |
| Members | Accounts on the request site, each with its own approval and limits | Requests → Members | Resource | Admin | None | — | Security |

## On each resource

| Name | Description | Where | Scope | Level | Default | Dependencies | Impact |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Watched (monitored) | Look for this title, season or episode | Title page | Resource | Basic | On when added | — | Behaviour |
| Quality profile | The profile a title uses | Title page | Resource | Basic | Root folder's or rule's | Profiles | Behaviour |
| Upgrades | Upgrade this title or not, overriding the app-wide choice | Title page | Resource | Advanced | As app-wide | — | Behaviour |
| Space saver | Keep only the next N episodes past what's watched | Show page | Resource | Advanced | Off | Matinee watch history | Data |
| Second copy | Keep a second copy (such as 4K beside 1080p) | Title page | Resource | Advanced | Off | A second root folder | Data |
| Edition | Which cut of a movie to keep | Movie page | Resource | Advanced | Any | — | Behaviour |
| Music quality | FLAC first, FLAC only, or MP3 | Music page | Application | Basic | FLAC first | — | Behaviour |

## Environment only

Set on the server, shown read-only where relevant.

| Name | Description | Level | Default |
| --- | --- | --- | --- |
| `BOXOFFICE_ADDR` | Address and port to listen on | Admin | `:7272` |
| `BOXOFFICE_DATA_DIR` | Data folder | Admin | `/data` in the image |
| `BOXOFFICE_DOWNLOADS_DIR` | Downloads folder | Admin | `downloads` in the data folder |
| `BOXOFFICE_DATABASE_URL` | PostgreSQL address; empty means SQLite | Admin | empty |
| `BOXOFFICE_TMDB_KEY`, `BOXOFFICE_API_KEY` | Keys set from outside | Admin | empty |
| `BOXOFFICE_SKIP_SIGN_IN_CODE` | Skip the one-time code when its app and recovery codes are lost | Admin | off |
| `BOXOFFICE_DEBUG`, `BOXOFFICE_LOG_FORMAT` | Log detail and format | Developer | off, text |
| `BOXOFFICE_ANNOUNCE` | Let Matinee find Box Office on the network | Admin | on (`false` turns it off) |
| `BOXOFFICE_REFRESH_EVERY` | How often title details are refreshed from TMDB | Advanced | 12 hours |
| `BOXOFFICE_EBOOK_CONVERT` | Ebook converter for e-readers | Advanced | — |
| `BOXOFFICE_*_URL` | Data sources (MusicBrainz, Open Library, Wikidata, game art and the like) | Developer | the public services |
