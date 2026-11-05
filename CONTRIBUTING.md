# Contributing

Thanks for wanting to help. Box Office is young, so a small fix lands quickly and you'll see it in the next build of `main`.

## Find something to work on

- [**good first issue**](https://github.com/yeixio/boxoffice/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22): small and well-scoped, with what "done" looks like and the file to start in.
- [**help wanted**](https://github.com/yeixio/boxoffice/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22): open for anyone. Check the `difficulty/easy`, `difficulty/medium` and `difficulty/hard` labels.
- The [roadmap](ROADMAP.md) lists what's planned.
- `area/*` labels say which part of the code an issue touches: `area/move-in`, `area/search`, `area/downloader`, `area/library`, `area/server`, `area/web`, `area/matinee`, `area/deploy`, `area/ci`.
- `needs design` means the issue wants a short written plan, agreed in the comments, before code. Writing that plan is a great contribution in itself.

Comment on an issue before you start, so two people don't do the same work. If nobody replies within a few days, go ahead. For a `difficulty/hard` issue, sketch your approach in the comments first.

Not writing code? Moving in from your Radarr or Sonarr and telling us what didn't come across, or what an odd indexer or news server did, is just as useful.

## Get it running

You need Go 1.26+ and Node 22 with pnpm.

```sh
git clone https://github.com/yeixio/boxoffice.git
cd boxoffice
make run    # builds the web UI and server, serves http://localhost:7272 with data in ./data
```

Open http://localhost:7272 and make the admin account.

For web work, keep `make run` going and run `make dev` in a second terminal: the web UI hot-reloads against your server.

A TMDB key is optional for most work ([how to get one](README.md#faq)). To try Move in, run Radarr or Sonarr in Docker with a few titles; to try downloads, you need a news server account and an indexer. Keep your own keys out of commits, screenshots and issues.

## Where things are

```
cmd/boxoffice          entry point and flags (internal/config)
internal/api           HTTP API for the web UI, auth, serves the UI
internal/arrapi        Radarr- and Sonarr-compatible API that Matinee talks to
internal/migrate       Move in from Radarr, Sonarr and NZBGet
internal/search        Newznab, RSS sync, library patrol, indexer pacing
internal/downloader    download queue, data cap, checks, moving into the library
internal/usenet        nntp, yenc, nzb, par2, unpack, screen
internal/catalog       movies, shows, episodes and TMDB refreshes
internal/store         SQLite / PostgreSQL storage and migrations
web/src                React web UI (pages/, plus shared ui.tsx, api.ts, sort.tsx)
deploy/helm/boxoffice  Helm chart
```

## Test and lint

```sh
make test    # Go tests (SQLite), web type checks and tests
make lint    # go vet and gofmt
```

CI also runs the Go tests on PostgreSQL. To do that locally:

```sh
BOXOFFICE_TEST_DATABASE_URL=postgres://user:pass@localhost:5432/postgres?sslmode=disable make test-postgres
```

Run `gofmt -w` on Go files you touch. If you change the Helm chart, run `make helm-lint`.

## Style

- Follow the code around you: small packages, plain functions, short comments that say why.
- The same SQL runs on SQLite and PostgreSQL. New tables go in `internal/store/schema.sql` with `CREATE TABLE IF NOT EXISTS`; a new column on an existing table is a new entry in the added-columns list in `internal/store/store.go`, with a default, so existing databases pick it up.
- `/radarr` and `/sonarr` are what Matinee depends on. Add fields and routes; don't rename or remove them.
- **Never change the old app during Move in.** Box Office only reads from Radarr, Sonarr and NZBGet, apart from asking them for a backup of themselves.
- **Nothing is deleted without a rule the user can see or a click they made.** Any new way of deleting files plans first, shows what and how much, and deletes only what it showed.
- **Detect what you can; explain what you can't.** If Box Office can work out an address, a path or a limit, it should. When someone must type something (an API key, a sign-in), the form says step by step where to find it.
- In the web UI, use the colors and spacing already in `web/src/index.css` and the parts in `web/src/ui.tsx`, handle loading, error and empty states, and check your change at 390 px wide, in light and dark.
- UI text is short and plain: "Add movie", not "Click here to add a new movie to your library".

## Pull requests

- Keep each pull request to one change, and link the issue it fixes (`Fixes #123`).
- Say how you tested it. For UI changes, add a screenshot, and a phone-width one if the layout changed.
- CI must be green: Go on SQLite and PostgreSQL, the web tests and build, the Helm checks and the Docker build.
- A maintainer reviews within a few days. Requested changes are normal, not a rejection.
- Add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for anything a user would notice.

Using AI tools to help is fine. You're responsible for what you submit, so read and test it before you open the pull request.

## Issues

Use the forms when you open an issue: bug, move-in report, or feature. Remove passwords, API keys and private URLs from logs.

Security problems go through [SECURITY.md](SECURITY.md), not a public issue. Code of Conduct reports go to conduct@yeix.io (see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)).

## Labels

Labels are defined in `.github/labels.yml`. Maintainers sync them with `scripts/sync-github-labels.sh`, which needs the GitHub CLI and permission to manage labels.
