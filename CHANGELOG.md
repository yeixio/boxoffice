# Changelog

What changed in Box Office, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). What's under **Unreleased** ships in the `main` image as it merges; releasing renames it to the version, such as `## [0.1.0] - 2026-11-06`, and that section becomes the GitHub release's notes (see [Releasing](README.md#releasing)).

## Unreleased

### Added

- **Versioned releases.** Pressing Run workflow under Actions → Release tags the next version, publishes `ghcr.io/yeixio/boxoffice:<version>`, `:<major>.<minor>` and `:latest`, pushes the Helm chart to `oci://ghcr.io/yeixio/charts/boxoffice`, and opens a GitHub release with this changelog's notes. `:main` stays the image of what just merged.

- **Box Office speaks Matinee's 32 languages,** in the admin pages and on the request site, right to left for Arabic, Hebrew, Persian and Urdu. Each admin and each member picks theirs from their account menu, or follows the browser. Dates, numbers and sizes follow the language too. The translations are machine-made for now and say so under the picker; messages written by the server, like error details and logs, stay in English.
- **A request site for family and friends,** in place of Overseerr: at `/request` or on its own host name, with your name, colour and logo. People sign in with their Matinee account or one made here, ask for movies and shows, follow them to ready to watch, and report problems. Admins approve or decline on the new **Requests** page, which also has problem reports, the people who can sign in, automatic approval and weekly limits.
- **More for Matinee when it's paired with Box Office** ([matinee#297](https://github.com/yeixio/matinee/issues/297)): live download progress with an ETA for titles on the way (`GET /{radarr,sonarr}/api/v3/boxoffice/activity`), **another edition of a movie** (Director's Cut, Extended…) to replace its copy or to keep next to it, named `{edition-…}` (`/radarr/api/v3/boxoffice/editions`), and **a better copy** or **a replacement for a bad one**, which blocklists the bad release (`POST /{radarr,sonarr}/api/v3/boxoffice/upgrade`). A movie replaced by an edition stays that edition in later upgrades. Each is advertised in `boxOfficeFeatures` (`progress`, `editions`, `upgrades`), so Matinee only shows what Box Office offers.
- **Search for and download ebooks and audiobooks.** Each book on an author's page has Look for it and Choose a release; wanted books are also searched for by the hourly search. Box Office searches the Newznab ebook (7020) and audiobook (3030) categories, prefers EPUB and M4B, and passes over comics, collections and the wrong format. Finished downloads are screened like movies (a release with no ebook or audio in it is quarantined), then moved to `<root>/<Author>/<Title> (<Year>)`, with Move into library when that's off or fails. Books already in that layout in a root folder count as on disk when an author is added or refreshed. Authors can be set to **Do not touch**.
- **Ebook and audiobook root folders can be scanned too:** each folder is taken as an author, matched on Open Library, and the authors picked go on the list, with their books already on disk counted as there. Nothing on disk is touched.
- **Official images come with a TMDB key,** so posters and details work with nothing to paste. Settings → TMDB shows **Built in**, and a key saved there or set in `BOXOFFICE_TMDB_KEY` still wins ([#51](https://github.com/yeixio/boxoffice/pull/51)).
- **Books and Audiobooks:** follow authors from Open Library and pick which of their books you want as ebooks, audiobooks or both, once either is turned on in Settings ([#50](https://github.com/yeixio/boxoffice/pull/50)). Searching and downloading them comes next.
- **Scan a root folder for what's on disk but not on the list.** Each movie, TV and music root folder in Settings has a Scan button: it runs in the background, matches each folder to TMDB or MusicBrainz, and lets you check the matches and add the ones you pick where they are, with their files. Nothing is moved, renamed or deleted.
- **Discover, the new home page:** what's trending, popular and coming soon on TMDB, recommendations based on what's in your library, and genres and networks to browse. Every poster says whether it's on disk, partly on disk, wanted or paused, and ones that aren't here can be added in one click with your usual profile and the recent-seasons default, or with other choices from the title's card. Matinee can show the same rows in its Discover tab in place of Overseerr, through `/{radarr,sonarr}/api/v3/boxoffice/discover`.
- **Music:** follow artists from MusicBrainz and pick which of their albums, EPs and singles you want, once Music is turned on in Settings ([#41](https://github.com/yeixio/boxoffice/pull/41)). Searching and downloading them comes next.
- **Music, audiobooks, ebooks and comics can be turned on in Settings,** each off by default, with its own root folders and one-click suggestions for where the library is mounted ([#38](https://github.com/yeixio/boxoffice/pull/38)). Adding and searching for them comes next.
- **New shows watch only their last 3 aired seasons, and any still to come,** so a long-running show doesn't flood the library patrol and upgrades. The rule and the number of seasons are in Settings → New shows, and can be changed per show when adding it. Matinee requests follow the same default; shows already on the list and ones brought in by Move in keep what they have. Specials stay off.
- **Removing titles can delete their files from disk,** showing the folder, file count and size first, and deleting only what it showed ([#28](https://github.com/yeixio/boxoffice/pull/28)). Recycle folders can be emptied from Settings, or after a number of days.
- **Downloads housekeeping:** failed downloads that can't be repaired are deleted, quarantine has its own section with release, delete and auto-delete after a number of days, long lists are paged, and the queue is summed up at the top ([#27](https://github.com/yeixio/boxoffice/pull/27)).
- Bulk actions on the Downloads queue and history ([#26](https://github.com/yeixio/boxoffice/pull/26)).
- A file browser for the downloads folder: move a folder into a title, release from quarantine, delete, download ([#25](https://github.com/yeixio/boxoffice/pull/25)).
- Matinee finds Box Office by itself, through `GET /api/v1/status` and its own entry in Matinee's Settings → Add-ons ([#23](https://github.com/yeixio/boxoffice/pull/23)).
- TV upgrades, and **Choose a release** for an episode or a whole season ([#22](https://github.com/yeixio/boxoffice/pull/22)).
- **RSS sync, library patrol pacing, a monthly data cap and block accounts** on news servers; searches stay under each indexer's daily limits ([#21](https://github.com/yeixio/boxoffice/pull/21)).
- Searches for missing titles and quality upgrades on their own, every hour ([#20](https://github.com/yeixio/boxoffice/pull/20)).
- Sorting for Movies, Shows, download history and releases, with ratings, size on disk and next and last episodes ([#19](https://github.com/yeixio/boxoffice/pull/19)).
- Notifications about downloads, kept for Matinee to pass on to the people who asked ([#15](https://github.com/yeixio/boxoffice/pull/15)).
- Health: server monitoring, a log viewer, a debug-logging switch, alerts and Prometheus metrics ([#14](https://github.com/yeixio/boxoffice/pull/14)).
- Unpack progress, **Move into library** later, and a check that a release is really as long as the movie or episode ([#13](https://github.com/yeixio/boxoffice/pull/13)).
- **Unpack again** without downloading again ([#11](https://github.com/yeixio/boxoffice/pull/11)).
- Unpacking RAR sets whose file names don't say what's inside, and **quarantine** for downloads that hold executables, disguised files or no video ([#10](https://github.com/yeixio/boxoffice/pull/10)).
- **Box Office's own Usenet downloader,** Newznab release search, and the Downloads page ([#9](https://github.com/yeixio/boxoffice/pull/9)).
- The Health page: problems Box Office finds, and how to fix each ([#7](https://github.com/yeixio/boxoffice/pull/7)).
- **Finish moving in** ([#6](https://github.com/yeixio/boxoffice/pull/6)).
- Move in looks over apps in the background, with a tray showing progress ([#4](https://github.com/yeixio/boxoffice/pull/4)).
- Move in finds the old app and its folders by itself ([#2](https://github.com/yeixio/boxoffice/pull/2)), and follows an app's URL base ([#3](https://github.com/yeixio/boxoffice/pull/3)).
- **The first Box Office:** the server, the web UI, the Radarr- and Sonarr-compatible API for Matinee, and a step-by-step move-in from Radarr, Sonarr and NZBGet that never changes the old app ([#1](https://github.com/yeixio/boxoffice/pull/1)).

### Changed

- **Replaced copies are kept in the recycle folders for 7 days** unless Settings says otherwise; before, they stayed until someone emptied them. Choose **Never (empty them yourself)** in Settings → Recycle folders to keep the old behaviour.

- **The sections moved to a sidebar on the left** on a computer, grouped into Library, Status and Setup, so they no longer crowd the top. It narrows to icons on a smaller screen, or with Icons only, which it remembers. On a phone the menu stays as it was, now with the same groups. Sections for media types that are off stay hidden.
- **Automatic upgrades are off by default.** RSS sync and the library patrol still look for missing titles by themselves; turn on Settings → Missing titles and upgrades → Automatic upgrades to have them go after better copies too. Looking for an upgrade from a title's page works either way. Each movie and show can turn automatic upgrades on or off for itself from its page, and **Most automatic upgrades a month** caps how many Box Office grabs by itself.

### Fixed

- **Disk space alerts don't repeat.** One is sent when a disk reaches the alert's percent, then again only when it's 5 points fuller (90%, then 95%) or at 98%, in place of every day. Restarting Box Office no longer sends it again, and a disk that dips a point or two under and back doesn't count as clearing up and filling again ([matinee#308](https://github.com/yeixio/matinee/pull/308) groups the rest in Matinee's bell).
- An episode whose video file has a meaningless name is matched by the download's name ([#24](https://github.com/yeixio/boxoffice/pull/24)).
- An import no longer fails when the original can't be removed from a downloads share ([#18](https://github.com/yeixio/boxoffice/pull/18)).
- A release's length is judged by its main video, so samples and extras don't quarantine it ([#16](https://github.com/yeixio/boxoffice/pull/16)).
- RAR 3 and 4 volume numbers are read from the right flag ([#12](https://github.com/yeixio/boxoffice/pull/12)), and old-style `.r00` naming is followed ([#11](https://github.com/yeixio/boxoffice/pull/11)).
- Path maps are found for every library, even with one typed in ([#8](https://github.com/yeixio/boxoffice/pull/8)).
- Move in: separate forms per app, NZBGet detection, Matinee's root folder, and a menu on phones ([#5](https://github.com/yeixio/boxoffice/pull/5)).
