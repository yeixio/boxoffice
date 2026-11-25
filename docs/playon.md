# PlayOn Cloud

[PlayOn Cloud](https://www.playon.tv/cloud) records shows and movies from
streaming services (Netflix, Hulu, Disney+, Max, Peacock, Paramount+ and
others) on PlayOn's own computers, so you end up with an ordinary MP4 file.
Box Office can bring those recordings into the library by itself: you queue
recordings in the PlayOn Cloud app as usual, and once each one is finished
Box Office downloads it and files it like any other download.

## Setting it up

1. Sign up for PlayOn Cloud and record something in its phone app.
2. In Box Office, open **Settings → Downloads → PlayOn Cloud**.
3. Enter the email address and password you sign in to PlayOn with and
   press **Test**. It says how many recordings are ready to download.
4. Turn on **Bring in recordings** and press **Save**.
5. In the PlayOn Cloud app, turn off downloading recordings to your phone
   (**Auto-Download Recordings on Wi-Fi**), so they aren't downloaded twice.

The password is kept in Box Office's database, like news server passwords,
and is never sent back to the browser.

## What happens to a recording

Every 15 minutes (and when you press **Check now**) Box Office asks PlayOn
for the account's recordings:

- A recording PlayOn is still making is left alone; the card counts what's
  queued in PlayOn.
- A finished one is downloaded into the `playon` folder in the downloads
  folder, one at a time, oldest first, and named for what it is
  (`The Flash - S09E03 - Rogue Air.mp4`, `Arrival (2016).mp4`) from what
  PlayOn says about it.
- It's then filed the way **Import files** files things: Box Office works
  out which show or movie it is on TMDB, adds the title if it isn't on the
  list (shows are added with nothing else watched), and moves the file into
  the title's folder with the library's naming.
- When Box Office isn't sure what it is, the file waits on the **Import
  files** page with what it found, for you to check and import.
- A download that fails is tried again at the next checks, three times in
  all, then marked **Failed**; **Retry** tries again. **Skip** leaves a
  recording in PlayOn.

Each recording is downloaded once. PlayOn deletes recordings after 7 days
unless the account pays for storage, so the card warns when one that hasn't
come in is about to go.

Downloads count toward the monthly data cap (**Settings → Searching →
Data cap**), and wait when it's used up. They don't follow the download
speed schedule.

## Limits

- **Unofficial.** PlayOn has no published API. Box Office uses the one its
  own apps use (the same as the open-source
  [cs.app.playon](https://pypi.org/project/cs.app.playon/) client), so a
  change on PlayOn's side may break this until Box Office is updated. The
  card shows PlayOn's error when that happens.
- **No queuing from Box Office yet.** Recordings are queued in the PlayOn
  Cloud app. Asking PlayOn to record a title from Box Office would need
  PlayOn's private calls for that, which nobody has documented.
- **PlayOn Home isn't covered.** PlayOn Home runs on a Windows PC and saves
  recordings into a folder. Until Box Office can watch a folder by itself,
  use **Import files → Copy from the server** on that folder.
- **Quality.** Recordings are captured as they play, at 720p or 1080p, so
  they're about as good as a web download, not a disc. They're filed like
  any other copy, so a profile with upgrades on may still replace one.
- **One account.** Changing the email address, or **Unlink account**,
  forgets the old account's recordings.
- Matinee isn't told about each recording separately yet; it sees the
  title once it's in the library, like anything else imported.

## For other apps

`GET /api/v1/playon` (state, recent recordings), `PUT /api/v1/playon`
(`on`, `email`, `password`, `forget`), `POST /api/v1/playon/test`,
`POST /api/v1/playon/check`, `POST /api/v1/playon/recordings/{id}/retry`
and `/skip`.
