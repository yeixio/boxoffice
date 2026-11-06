# Roadmap

Where Box Office is headed. Nothing here has a date; the order is roughly the order things will happen. Want to help with one? Look for its issue, or open one and say so. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Now: dependable for movies and TV

Box Office already replaces Radarr, Sonarr and NZBGet for one household. The work now is making that solid for everyone else.

- **Tagged releases,** so the Helm chart and images have versions, with release notes from the [changelog](CHANGELOG.md).
- **Richer status in Matinee:** download progress, editions and upgrades, shown on Matinee's posters and requests ([#17](https://github.com/yeixio/boxoffice/issues/17)).
- **More search rules:** a minimum age for releases, preferring corrected copies when a provider replaces one, and a limit on how often one title is downloaded again in a day.
- **A download speed schedule,** such as slower during the evening.
- **Move-in reports from more setups,** so unusual Radarr and Sonarr configurations come across cleanly. [Send one](https://github.com/yeixio/boxoffice/issues/new?template=move_in_report.yml).

## Next: everything a library needs

- **Torrents:** downloading through a torrent client, alongside Usenet. Torznab indexers and torrent clients already come across in Move in and are kept for this.
- **Requests without Matinee:** a simple, brandable page where everyone in the house asks for movies and shows and reports problems, separate from the admin pages. People using Matinee already do this in Matinee.
- **Native Matinee integration,** beyond the Radarr- and Sonarr-compatible API.

## Later

- Notification titles in more languages (they're English only for now).
- More places to send alerts and backups.
- Packages and NAS app-store listings, like Matinee's.

Ideas that aren't here yet are welcome as a [feature request](https://github.com/yeixio/boxoffice/issues/new?template=feature_request.yml).
