# Games

The **Games** section shows the video games you already have, on any of about
seventy systems: arcade sets for MAME; cartridge consoles and handhelds such as
the Super Nintendo, Game Boy and Mega Drive; disc consoles from the PlayStation
and Saturn to the PlayStation 5 and Xbox One; the Switch, 3DS, PSP and Vita; and
home computers, DOS and Windows PC games. Box Office finds each system's folder,
works out what every file is, checks whether each game is complete, and shows
them with box art, year, maker and players.

Box Office never moves, renames, rebuilds or deletes a game file. It only adds
new ones: games you import yourself with **Import games** (see
[Importing files](import.md)), and games you want that it downloads for you (see
[Adding a game](#adding-a-game)).

## Turning it on

1. In **Settings → Library**, turn on **Games** under **More than movies and
   TV**. It's off until you do, and nothing about games shows until then.
2. Under **Games root folders**, add the folder your games are in. Box Office
   suggests `/media/games`, `/data/media/games`, `/media/roms` and the like when
   it can see one.
3. Box Office starts looking through the folder straight away. **Games** in the
   sidebar shows how far it has got.

## Laying out the folder

A games root folder is either one system's folder, or a folder with a folder for
each system:

```text
/media/games/
  mame/        pacman.zip, puckman.zip, kinst.zip, kinst/kinst.chd …
  snes/        Super Metroid (Japan, USA) (En,Ja).zip …
  gba/
  psx/         Ridge Racer (USA).cue, Ridge Racer (USA).bin, Wipeout.chd …
  ps3/         BLUS30001/PS3_GAME/… , Game (USA).iso
  switch/      Some Game [0100ABCD12340000][v0].nsp …
  pc/          Some Game/Game.exe …
```

Box Office tells each folder's system by its name: the system's short name
(`mame`, `snes`, `n64`, `psx`, `ps3`, `switch`, `pc`), its full name (`Super
Nintendo`, `PlayStation 2`), the name RetroArch and libretro give it (`Nintendo
- Super Nintendo Entertainment System`), or the name of the emulator it's for
(`rpcs3`, `dolphin`, `yuzu`, `cemu`, `dosbox`). A folder named for nothing it
knows is told by its games: their extensions (`.sfc`, `.gba`, `.nsp`, `.3ds`…),
what's inside their zips, or what's written inside a disc image (see
[Discs, downloads and PC games](#discs-downloads-and-pc-games)). Zips that hold
no console's games are taken to be MAME sets. A folder it still
can't tell is listed on the Games page; rename it to the system's name and press
**Check again**.

One more level is looked through, so `/media/games/roms/snes` works too.

## MAME

Each set is a zip (or 7z) named by MAME's short name, such as `pacman.zip`. Box
Office reads the list MAME publishes with each release (`mame0289lx.zip`, the
output of `mame -listxml`) from MAME's releases on GitHub, and from it takes
each set's real title, year, manufacturer and players, and how sets lean on each
other:

- **Clones** (such as `pacman`, a clone of `puckman`) are grouped under their
  parent, so a game shows once with its other versions on its page.
- **BIOS and device sets** (`neogeo`, `qsound`) aren't games, so they're not
  listed, but a game whose BIOS or device set is missing says so.
- **CHDs** are looked for in the folder named for the set, such as
  `kinst/kinst.chd`, and checked by the SHA-1 in their header.
- **Merged, split or non-merged:** Box Office tells how your sets are put
  together from what's in them, and checks them the same way whichever it is.

Each set is checked file by file against the list, by the CRCs in the zip's
directory (nothing is unpacked), and is:

| Status | Meaning |
| --- | --- |
| Complete | Everything the set needs is there. |
| Missing files | Some files, a parent set or a CHD aren't there. |
| Wrong version for MAME 0.xxx | Some files have the right name but are from another version of MAME. |
| Needs a BIOS | The set is complete, but a BIOS or device set it needs isn't. |
| Not on the list | The list has no set of that name: it may have been renamed, or be for another emulator. |

Sets are checked against the newest MAME unless you pick the version you play
with under **MAME version** on the Games page; picking another checks every set
again. Versions from 0.162 on can be picked, as that's where MAME's
published lists start; an older set (a MAME 0.154 collection, say) is checked
against the version picked, and its renamed files and the device sets newer
versions split out show as missing.

### What "with problems" counts

Each system's **with problems** count is games, the same games as its **games**
count, not files. A MAME game has a problem when any of its sets has one, as
each clone is a game of its own. A console game has one only when none of its
versions is good: one good copy is all it takes to play it, so bad dumps, hacks
and translations kept beside it don't count. A merged archive (a GoodMerged
`.7z` holding every version of one game) is one game. Pressing the count opens
the system's games with **Has problems** on, and each game's page says what's
wrong with each of its versions. Genres come from a `catver.ini` in the MAME folder or the games root
folder, if you have one: MAME's list has none of its own.

## Cartridges

Cartridge and floppy games are matched by their contents, not their names: Box Office hashes
each file (inside a zip or 7z, the CRC is already written down) and looks it up
in [No-Intro](https://no-intro.org)'s list for the system, so a game named
anything is still found. NES files' iNES headers, old SNES copier headers and
Nintendo 64 files in either swapped byte order are allowed for. Files over
256 MB aren't hashed.

A file No-Intro knows is **Complete**. One named like a game on the list but
different is a **Bad copy**. One the list doesn't know at all (a hack, a
translation, a homemade game) is **Not on the list**, which is fine. Versions of
the same game for other regions or revisions are grouped together.

No-Intro's lists, and each game's year, publisher, genre and players, come from
[libretro's database](https://github.com/libretro/libretro-database), and are
downloaded again each month.

The systems checked this way are the NES, Famicom Disk System, Super Nintendo,
Satellaview, Sufami Turbo, Nintendo 64 and 64DD, Game Boy, Game Boy Color, Game
Boy Advance, Virtual Boy, Pokémon Mini, Nintendo DS and DSi, SG-1000, Master
System, Game Gear, Mega Drive / Genesis, 32X, PC Engine / TurboGrafx-16 and
SuperGrafx, Neo Geo Pocket and Pocket Color, WonderSwan and WonderSwan Color,
Atari 2600, 5200, 7800, 8-bit computers, Lynx, Jaguar and ST, ColecoVision,
Intellivision, Vectrex, Odyssey², Channel F, Supervision, Game.com, MSX and
MSX2, Commodore 64 and Amiga, Amstrad CPC, ZX Spectrum, Apple II and X68000.

## Discs, downloads and PC games

Disc images and downloaded games are too big to hash, so Box Office reads what's
written inside them instead: the serial on a PlayStation disc (`SLUS-00067`), the
title ID in a Switch, PS3, PS4, Vita or 3DS file, the game ID of a GameCube or
Wii disc, the title in an Xbox or Xbox 360 game. It looks that up in the
system's list, and if there's nothing to read or the list doesn't have it, the
name written inside the game, then the file's name. A game found this way is
**Recognized**: it is the game the list says, but wasn't checked byte for byte.

| Systems | List | Read from inside |
| --- | --- | --- |
| PlayStation, PS2, PSP | [Redump](http://redump.org) | Serial from `SYSTEM.CNF` or `PARAM.SFO`, in `.iso`, `.bin`/`.cue`, `.cso`, `.pbp` |
| PlayStation 3 | Redump, No-Intro (PSN) | `PS3_GAME/PARAM.SFO` in a folder or `.iso`, the content ID of a `.pkg` |
| PS Vita | No-Intro | `sce_sys/param.sfo` in a folder or `.vpk` |
| PlayStation 4, PlayStation 5 | none | `param.sfo` / `param.json` in a folder or `.pkg` |
| GameCube, Wii | Redump | The game ID, in `.iso`, `.gcm`, `.rvz`, `.wia`, `.wbfs`, `.ciso`, `.gcz` |
| Wii U | none | `meta/meta.xml` in a game folder |
| Switch | The eShop's (see below) | The title ID in the file's name, or inside an `.nsp` |
| 3DS | No-Intro | The product code in `.3ds`, `.cci`, `.cia` |
| Saturn, Sega CD, Dreamcast | Redump | The disc header, in `.cue`/`.bin`, `.gdi` |
| Xbox, Xbox 360 | Redump | The title in `default.xbe` / `default.xex`, in a folder, `.iso` or a 360 download folder |
| Xbox One / Series | none | The game folder's name |
| PC Engine CD, PC-FX, Neo Geo CD, 3DO, CD-i, Jaguar CD, Amiga CD32 and CDTV, PC-98, FM Towns | Redump | The disc's own header, where it has one |
| AAE (Another Arcade Emulator) | none | The set's file name, in the `aae` folder. AAE's sets share MAME's names but aren't MAME's, so they're kept apart |
| DOS, Windows PC | none | The game folder's name; a disc image (`.iso`, `.bin`/`.cue`, `.img`, `.mdf`, `.nrg`) with `AUTORUN.INF` or a setup program is a PC game's |

A disc in several files (a `.cue` and its `.bin` tracks, a `.gdi`, a `.m3u` of
discs) is one game. A game that's a folder, such as a dumped PS3 game, a Wii U
game or a PC game, is one game too: put each in its own folder in the system's
folder. A PC game on several CDs is one game: keep its disc images together in
its folder. CHD files and Switch `.xci` cartridges can't be read inside (CHDs are
compressed, cartridges are locked), so they're known by their names: name them
as the list does.

Systems with no list (PlayStation 4 and 5, Wii U, Xbox One, PC, DOS) are always
**Recognized**, by the name written in the game or on its folder. Steam's own
list isn't used, as it needs an account.

**Updates and DLC** (a Switch update `.nsp`, a PS4 patch `.pkg`) aren't games of
their own: they're listed with the game's name and *(Update)* or *(DLC)*, and
don't count toward having the game.

A file whose contents say it's for another system (a PlayStation disc in the
PS2 folder) says so under **Good to know**.

### The Switch

The Switch's list is the eShop's, gathered by the
[titledb](https://github.com/blawar/titledb) project: every game's title ID,
name, publisher, release date, players and icon. It's about 90 MB, downloaded
the first time a Switch folder is checked and again each month, and kept as just
the games (no updates or DLC), which is far smaller. Name your files with their
title ID in square brackets, as most dumping tools do
(`Game Name [0100ABCD12340000][v0].nsp`); a file without one is matched by name.

## Adding a game

**Add a game** on the Games page looks a game up by name in the same lists Box
Office checks your games against: No-Intro's for cartridges, Redump's for discs,
the eShop's for the Switch and MAME's for arcade sets. A system with no list
(PC, PlayStation 4) takes the name you type, and Box Office looks for releases
with that name. It looks in the systems you have a folder for, or pick one under **All
systems** (a folder named for it is made when the first game goes in). Each game
shows its best version, usually the USA or World one, the newest revision; pick
another under **Version**. **Want it** adds it to **Wanted** on the Games page
and searches the indexers straight away. A game you have already says **In your
games**.

Searches look in the indexers' console categories and in Other (where older
systems' games are often filed), or for PC games in the PC categories. A release is passed over when it names another
system, is a set of many games (a romset, No-Intro or Redump pack), is something
else (a soundtrack, a game for a PC or another console, an update or DLC on its
own, a film), or is too big to be one game.
One that names the system comes first. **Choose a release** shows every release
found and why any was passed over, and grabs the one you pick. Wanted games are
looked for again by the hourly patrol, with everything else wanted.

Every download is checked before it goes in, the same way the games in your
folders are. A console game's file (loose, or inside a zip or 7z, even one with
other games in it) must have the hash No-Intro gives the version you want, or
another version of the same game; a MAME set must be named for the set and hold
at least one of its ROMs, and its parent, BIOS and device sets come in with it.
A disc or downloaded game must have the serial or title ID of the game you want
(or, with nothing to read inside, its name), and be for the right system; its
tracks come in with it, in a folder named for the game. A PC game comes in as its
whole folder.
One that isn't the game is quarantined, its release blocked, and the next one
looked for. The game goes into its system's folder named as the list names it
(`snes/Super Metroid (Japan, USA) (En,Ja).sfc`, `mame/pacman.zip`), the system is
checked, and the game leaves **Wanted**.

Usenet indexers carry far fewer single retro games than films, so many searches
find nothing; Box Office keeps looking, less often after a few misses. A system
on **Do not touch** takes no new games.

## Box art

Pictures come from [libretro's thumbnails](https://thumbnails.libretro.com): the
box art, or for a game without, its title screen or a screenshot. Switch games
have their eShop icon, and PSP, PS3, PS4 and Vita games without a picture
elsewhere show the icon inside the game; a MAME clone
without its own has its parent's. Each is downloaded the first time it's shown,
made small and kept in `<data>/games/art`. A game that isn't a version on the
list (a homebrew build, say) has the picture of a version with the same title.
A game with no picture anywhere shows its name over a picture of its system.

The system pictures are RetroArch's, from
[libretro/retroarch-assets](https://github.com/libretro/retroarch-assets), under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/); see
`web/public/systems/CREDITS.md`.

## Checking again

Box Office checks the games root folders when one is added, when you press
**Check again**, and by itself once a week. A system set to **Do not touch** (at
the bottom of its page) isn't looked at again until you turn that off, and keeps
what was found last time: use it while you're reorganizing a folder.

## No accounts needed

None of these sources needs an account or a key. If you'd rather Box Office
fetched them from a copy of your own, these settings point it elsewhere:

| Environment variable | Default | What it's for |
| --- | --- | --- |
| `BOXOFFICE_MAME_RELEASES_URL` | `https://github.com/mamedev/mame/releases` | Where MAME's lists are downloaded from: `<url>/latest` and `<url>/download/mame0289/mame0289lx.zip` |
| `BOXOFFICE_LIBRETRO_DB_URL` | `https://raw.githubusercontent.com/libretro/libretro-database/master` | A copy of libretro's database, for No-Intro's, Redump's and TOSEC's lists and game facts |
| `BOXOFFICE_TITLEDB_URL` | `https://raw.githubusercontent.com/blawar/titledb/master` | A copy of titledb, for the Switch's list (`<url>/US.en.json`) |
| `BOXOFFICE_GAME_ART_URL` | libretro's thumbnails, then the same on GitHub | Where pictures are looked for, comma separated. `{system}` is libretro's name for the system, `{repo}` the same with `_` for spaces, `{kind}` `Named_Boxarts`, `Named_Titles` or `Named_Snaps`, and `{name}` the game |
