# Changelog

What changed in Box Office, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions will follow [Semantic Versioning](https://semver.org/) once releases are tagged. Until then, everything is in **Unreleased** and ships in the `main` image as it merges.

## Unreleased

### Added

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

### Fixed

- An episode whose video file has a meaningless name is matched by the download's name ([#24](https://github.com/yeixio/boxoffice/pull/24)).
- An import no longer fails when the original can't be removed from a downloads share ([#18](https://github.com/yeixio/boxoffice/pull/18)).
- A release's length is judged by its main video, so samples and extras don't quarantine it ([#16](https://github.com/yeixio/boxoffice/pull/16)).
- RAR 3 and 4 volume numbers are read from the right flag ([#12](https://github.com/yeixio/boxoffice/pull/12)), and old-style `.r00` naming is followed ([#11](https://github.com/yeixio/boxoffice/pull/11)).
- Path maps are found for every library, even with one typed in ([#8](https://github.com/yeixio/boxoffice/pull/8)).
- Move in: separate forms per app, NZBGet detection, Matinee's root folder, and a menu on phones ([#5](https://github.com/yeixio/boxoffice/pull/5)).
