# Lists

Box Office can follow a list you keep somewhere else. What you put on the list is added to Box Office by itself, so adding a movie to your watchlist on your phone is enough to have it looked for at home.

It follows:

- **TMDB lists** that are public, such as `https://www.themoviedb.org/list/12345`.
- **Trakt watchlists and lists** of a public profile, such as `https://trakt.tv/users/NAME/watchlist` or `https://trakt.tv/users/NAME/lists/LIST`.
- **Plex watchlists**, through the watchlist's RSS feed (`https://rss.plex.tv/…`), which needs Plex Pass.

IMDb and Letterboxd lists can't be followed yet. Neither site has an API for them.

## Following a list

1. Open **Settings → Library → Lists** and choose **Follow a list**.
2. Paste the list's address, as your browser shows it, and choose **Check the list**. Box Office works out which site it's on and shows what it found.
3. Pick what to add (movies, shows or both), the quality profile and root folder for each, and whether to be asked first.
4. Choose **Follow a list**. It's read straight away, and every 6 hours after that.

### Trakt's client ID

Trakt asks every app for a client ID before it shows a list. The first time you paste a Trakt address, Box Office asks for one:

1. Sign in at trakt.tv, then open **Settings** and **Your API apps** (`trakt.tv/oauth/applications`).
2. Choose **New application**. Give it any name, such as Box Office, and type `urn:ietf:wg:oauth:2.0:oob` under **Redirect URI**. Leave the rest as it is.
3. Save the app, then copy its **Client ID** (not the Client Secret) and paste it into Box Office.

Box Office only reads public lists with it. It never signs in to your Trakt account.

### A Plex watchlist's feed

1. In Plex on the web (`app.plex.tv`), open your picture, then **Account settings**.
2. Open **Watchlist** and turn on the RSS feed. This needs Plex Pass.
3. Copy the address of your own watchlist's feed. It starts with `https://rss.plex.tv/`.

Anyone with that address can read your watchlist, so treat it like a password.

## What happens to each title

**Titles** on a list's row shows every title the list has had and what became of it:

- **Added**: Box Office added it for this list, with the list's profile and root folder, and started looking for it if the list says so.
- **Already here**: it was on Box Office's list before the list had it. Nothing changes.
- **Waiting for you**: the list is set to **Ask me first**. Choose **Add** or **Leave out**.
- **Removed here, not added again**: a list added it and you removed it from Box Office since. It stays off, whichever list has it, until you choose **Add**.
- **Left out**: you said no. **Add** changes your mind.
- **Couldn't be added**: with the reason, such as a missing root folder. It's tried again at the next check.

Taking a title off the list elsewhere doesn't remove it from Box Office.

## Limits

- A list adds at most 20 titles each time it's read, so a long list arrives over a few days instead of starting hundreds of searches at once. The rest follow at the next checks, or when you choose **Check now**.
- Only a list's first 2,000 titles are read.
- **Stop following** forgets the list. The titles it added stay.

## API

`GET /api/v1/lists`, `POST /api/v1/lists/look` (`{"address": "…"}`), `POST /api/v1/lists`, `PUT` and `DELETE /api/v1/lists/{id}`, `POST /api/v1/lists/{id}/check`, `GET /api/v1/lists/{id}/items`, `POST /api/v1/lists/{id}/items` (`{"kind": "movie", "tmdbId": 603, "add": true}`), `PUT /api/v1/lists/trakt` (`{"clientId": "…"}`).
