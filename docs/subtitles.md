# Subtitles

Box Office fetches subtitles for your movies and episodes in the languages you pick, the way Bazarr did. Turn it on in **Settings → Library → Subtitles**.

## What it does

- **New downloads** are looked up as soon as they're in the library.
- **The rest of the library** is gone through slowly, newest files first, at most **Files looked up a day** files a day (300 to start with). The card says how many are left and when it should be done.
- **It looks before it fetches.** Each file is looked inside with ffprobe (the first time only) and beside it, so a language a file already has, inside it or as a file such as `Movie.en.srt`, `Movie.eng.srt` or `Movie.English.sdh.srt`, is never fetched again. Forced subtitles, which only cover the foreign parts, don't count.
- **Subtitles are saved beside the video** as `Movie (2001).en.srt`, or `Movie (2001).en.sdh.srt` for ones for the deaf and hard of hearing, in UTF-8. Matinee, Plex, Jellyfin and other players find them there. When the video is renamed, moved or deleted, its subtitles go with it.
- **Nothing is replaced or deleted**, and titles on the **Do not touch** profile are left alone.

## Picking the best one

The subtitle made for this very file comes first (OpenSubtitles knows by the file's hash), then one made for the same release: the same group, then the same source (Blu-ray, WEB, HDTV) and picture size. Then trusted uploads and the most downloaded. Machine-translated subtitles, and forced ones, are never picked. **Prefer subtitles for the deaf and hard of hearing** decides between the two kinds when both fit.

## When none is found

A subtitle not found is looked for again later: every 6 hours while the file is less than two weeks old (subtitles for new releases turn up in the days after), then after a day, 3 days, a week, two weeks, and monthly after that. **Look for missing subtitles again** in Settings, and **Find missing subtitles** on a movie or show, look again now; asking from a title isn't held to the daily limit. Reporting in Matinee that a title's subtitles are missing or wrong looks for its missing ones at once too.

## Where subtitles come from

- **Podnapisi** needs no account, so it works as soon as subtitles are on.
- **OpenSubtitles** is the largest library. It needs a free API key, and a free account lets Box Office download 20 subtitles a day (VIP accounts get 1,000):
  1. Sign up for a free account at [opensubtitles.com](https://www.opensubtitles.com) and confirm your email address.
  2. Signed in, open [opensubtitles.com/consumers](https://www.opensubtitles.com/consumers).
  3. Choose **New consumer**, name it Box Office, and save it.
  4. Copy its API key into Box Office, with your account's username and password, and press **Check the login**.

  If Matinee already searches OpenSubtitles, the same API key works for both. Once the day's downloads are used up, subtitles found there wait until the next day (OpenSubtitles' day starts at midnight UTC) and the card says so.

## On each title

A movie's **File** card lists its subtitles by language: green when the file has them (hover to see whether they're inside it, beside it or downloaded, and from where), gold while they're being looked for. A show's episodes have a subtitles mark that's lit when an episode has every language.

## API

- `GET /api/v1/subtitles`: the settings (without the API key or password), the status, the languages there are and suggested ones.
- `PUT /api/v1/subtitles`: save the settings. An empty `apiKey` or `password` keeps the saved one; `forgetOpenSubtitles: true` removes the login.
- `POST /api/v1/subtitles/test`: check an OpenSubtitles login.
- `POST /api/v1/subtitles/again`: look for every missing subtitle again.
- `POST /api/v1/movies/{id}/subtitles`, `POST /api/v1/series/{id}/subtitles` (optionally `{"episodeIds": [...]}`): look for a title's missing subtitles now.
- Movie and episode files have `subtitles`: `[{language, state, provider, ...}]`, where `state` is `embedded`, `on_disk`, `fetched`, `missing`, `waiting` or `pending`.
