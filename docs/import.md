# Importing files

**Import files** in the sidebar brings in media you already have: a movie you
ripped, a season pack from a friend's drive, an album, an ebook, a folder of
game ROMs. Box Office works out what each one is, adds the title to your list
if it isn't there yet, and files it in your library with the name the library
uses, the same way it files a finished download.

## Adding files

There are three ways in, and you can mix them:

- **Upload files** sends files from the computer or phone you're using. You can
  also drop files anywhere on the page.
- **Upload a folder** sends a whole folder with what's in it, such as a season
  with its episodes or an album with its tracks.
- **From the server** lists the folders Box Office can see (it starts at
  `/media`). Tick the files and folders you want and press **Copy in**. Box
  Office copies them, so the originals stay where they are.

Uploads carry on while you look at other pages; closing the tab stops them.
Everything waits in the `imports` folder inside the downloads folder until it's
imported, so nothing is lost if Box Office restarts. A copy **From the server**
that a restart cuts off carries on by itself when Box Office starts again,
skipping the files it had finished; if the folder it was copying from can't be
read any more, the page says so and what was half copied is removed. An upload
cut off by a restart is removed too, so send it again.

## What Box Office guesses

Each file or folder gets a guess from its name and what's inside:

| What's in it | It's taken to be | Matched with |
| --- | --- | --- |
| Videos named with an episode, like `S01E02` or `1x02` | A show's episode, or a season when it's a folder | TMDB |
| Other videos | A movie | TMDB |
| Music files | An album, named `Artist - Album` | MusicBrainz |
| `.m4b` files, long audio files, or a name saying *audiobook* | An audiobook, named `Author - Title` | Open Library |
| `.epub`, `.mobi`, `.azw3`, `.pdf` and the like | An ebook, named `Author - Title` | Open Library |
| Game files (`.sfc`, `.gba`, `.nsp`, `.iso`, `.cue`…), CHDs, zips holding them, and game folders (a PS3 or PC game) | Games, by system | Their extension, what's in the zip, or the serial or title ID written inside |

A guess that doesn't quite agree with the name says **Check this**. Press
**Change** on any item to pick what it is yourself: choose the kind, then find
the movie, show, artist or author; for an album or a book, type its title too.
For games, pick the system, then say which game it is: type its name to find
it on the system's list and **Pick** it, or, when the list hasn't got it or the
system has none (PC games), **Use** the name you typed. The game goes in under
that name. Box Office fills this in itself when it can: a Switch download by
the title ID in it, a PlayStation disc by its serial. A folder of disc images,
such as an old Windows game on seven CDs, is one game, and goes in whole; discs
that start by themselves or have a setup program on them are told to be a PC
game's.

Music, ebooks, audiobooks and games can only be imported once they're turned on
in **Settings → Library**, and each needs a root folder.

## Importing

**Import** files every item that's ready:

- A movie or show not on your list is added first, with your default quality
  profile and root folder. A show added this way isn't watched for new episodes;
  turn that on from its page if you want it.
- An artist or author not on your list is added the same way, unwatched.
- The file is then moved into the title's folder and named as the library names
  it, just as **Move into library** does for a download.
- Games are copied into their system's folder in your games root folder
  (`snes/`, `gba/`, `mame/`…), then Games checks them. A disc's tracks (a
  `.cue` and its `.bin` files) go together in a folder named for the game, and a
  game that's a folder is copied whole. A game that's already there with the
  same size is skipped.
- A folder of games can be a whole emulator, such as a MAME or AAE install.
  Box Office knows one by its program (`mame.exe`, `mame0154b.exe`, `aae.exe`)
  and shows it as the item's name, with MAME's version when the program's name
  or its `whatsnew.txt` says. Every archive in its `roms/` folder is that
  emulator's game, whatever is inside: MAME's go in `mame/`, AAE's in `aae/`,
  apart from MAME's because AAE names its different sets the same. The
  emulator's own folders (`samples/`, `artwork/`, `snap/`, `cfg/`, `nvram/`,
  `hiscore/`…) are left out, and its programs, settings and notes are left
  without a mention. Only files that might be games are ever listed as ones
  Box Office couldn't tell.
- A game that's named like a different file already in the games folder isn't
  put over it. The rest of the folder still goes in, and the item lists the
  games left in the imports folder for that reason, and any files that might be
  games it couldn't tell, so you can look at them and **Remove** the item when you're done.

Each item says where it went. **Remove** deletes an item's copy from the imports
folder without importing it.

## Games need a writable folder

Box Office normally only reads game files. Importing is the one time it writes
to the games root folder, so that folder has to be writable by Box Office: in
Kubernetes, don't mount it `readOnly`. If it isn't writable, the item says so
and nothing is changed.

## Big files

Uploads go straight to disk, so there's no size limit in Box Office itself. A
reverse proxy in front of it may have one: for nginx, set
`client_max_body_size 0;` on Box Office's location. For very large collections
on the same server, **From the server** is quicker than uploading.

## Downloading from an NZB of your own

On the **Downloads** page, **Add NZB** takes an NZB file (choose it, or drop
it anywhere on the page) or a link to one. For a link from your indexer,
right-click the NZB download button on the release's page and copy the link;
if the indexer is one you set up in Settings, Box Office adds your API key
when the link doesn't have it.

Box Office reads the NZB first and says what it thinks the release is. Then
choose what happens once it's downloaded:

- **File it in the library** (the default): once it's unpacked and checked,
  it goes to the imports folder and is filed the way Import files does it,
  worked out from its files. If Box Office isn't sure what it is, it waits on
  the Import files page for you to check, and the download's history links
  there.
- **File it as** a movie, show, album, ebook, audiobook or game: the same,
  but it's checked and looked up as that. A release that doesn't hold what it
  was said to be (no ebook in an "ebook") is quarantined.
- **Leave it in the downloads folder**: nothing is moved.

Filing happens even when moving downloads into the library by itself is off,
since you asked for it.

To download an NZB for a title already on your list, open **Choose a
release** on the movie, season, episode, album, book or wanted game, and use
**Upload an NZB** at the bottom. It's downloaded for that title and moved in
like a release Box Office found itself.
