# Security policy

## Supported versions

Security fixes go into `main`, and from the first tagged release on, into the latest release. Older tags are not maintained as a separate support line.

## Reporting a vulnerability

Please don't report security vulnerabilities in public issues.

Use GitHub's private vulnerability reporting for this repository:

1. Open the repository's **Security** tab.
2. Select **Report a vulnerability**.
3. Include the affected version (shown at the bottom of each page, or the image tag), steps to reproduce, what you expected and saw, and the likely impact.

Remove passwords, session cookies, API keys, news server sign-ins and private URLs from anything you paste. We'll reply in the advisory, and may ask for more detail while we work on a fix. Please give us a reasonable time to fix it before disclosing publicly. There is no bug bounty.

## Sensitive areas

Box Office holds keys to other people's accounts and writes to your library, so these deserve extra care in review:

- **Sign-in, sessions and the API key** (`internal/api`, `internal/arrapi`): password hashing, session cookies, admin-only routes, and the key Matinee and other apps use at `/radarr` and `/sonarr`.
- **Secrets it keeps:** indexer API keys, news server passwords, the TMDB key, the S3 keys and backup passphrase, and the secrets read from Radarr and Sonarr backups during Move in. They must stay out of logs, API responses and unencrypted exports.
- **Downloaded files are untrusted** (`internal/usenet`, `internal/downloader`): NZBs, par2 sets and RAR archives come from strangers. Unpacking must never write outside the download's folder, follow a link out of it, or let a crafted archive exhaust memory or disk. Release screening (`internal/usenet/screen`) is what keeps executables and disguised files out of the library.
- **File paths** (`internal/downloader/files.go`, `internal/api/removal.go`, the folder picker at `GET /api/v1/fs`): the downloads file browser works inside an `os.Root`; deleting a title's files is limited to what was shown and refuses root folders, `.recycle`, symlinks and folders holding another title. Any new path must keep those guarantees.
- **Outgoing requests** (Move in, indexers, news servers, Matinee switch-over, backups): addresses come from admins, but responses come from other software and must be treated as untrusted.
