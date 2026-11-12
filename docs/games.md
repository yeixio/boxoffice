# Games

The **Games** section shows the video games you already have: arcade sets for
MAME, and console games such as the Super Nintendo, Game Boy and Mega Drive. Box
Office finds each system's folder, works out what every file is, checks whether
each game is complete, and shows them with box art, year, maker and players.

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
  n64/
```

Box Office tells each folder's system by its name: the system's short name
(`mame`, `snes`, `n64`, `gba`, `genesis`), its full name (`Super Nintendo`), or
the name RetroArch and libretro give it (`Nintendo - Super Nintendo
Entertainment System`). A folder named for nothing it knows is told by its
games: their extensions (`.sfc`, `.gba`, `.z64`…), or what's inside their zips.
Zips that hold no console's games are taken to be MAME sets. A folder it still
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
again. Genres come from a `catver.ini` in the MAME folder or the games root
folder, if you have one: MAME's list has none of its own.

## Consoles

Console games are matched by their contents, not their names: Box Office hashes
each file (inside a zip or 7z, the CRC is already written down) and looks it up
in [No-Intro](https://no-intro.org)'s list for the system, so a game named
anything is still found. NES files' iNES headers, old SNES copier headers and
Nintendo 64 files in either swapped byte order are allowed for. Files over
256 MB, such as disc images, aren't hashed.

A file No-Intro knows is **Complete**. One named like a game on the list but
different is a **Bad copy**. One the list doesn't know at all (a hack, a
translation, a homemade game) is **Not on the list**, which is fine. Versions of
the same game for other regions or revisions are grouped together.

No-Intro's lists, and each game's year, publisher, genre and players, come from
[libretro's database](https://github.com/libretro/libretro-database), and are
downloaded again each month.

The consoles Box Office knows are the NES, Super Nintendo, Nintendo 64, Game Boy,
Game Boy Color, Game Boy Advance, Virtual Boy, Mega Drive / Genesis, Master
System, Game Gear, PC Engine / TurboGrafx-16, Atari 2600, 7800 and Lynx, Neo Geo
Pocket and Pocket Color, and WonderSwan and WonderSwan Color. Disc-based
systems (PlayStation, Saturn, Dreamcast) aren't yet.

## Adding a game

**Add a game** on the Games page looks a game up by name in the same lists Box
Office checks your games against: No-Intro's for consoles and MAME's for arcade
sets. It looks in the systems you have a folder for, or pick one under **All
systems** (a folder named for it is made when the first game goes in). Each game
shows its best version, usually the USA or World one, the newest revision; pick
another under **Version**. **Want it** adds it to **Wanted** on the Games page
and searches the indexers straight away. A game you have already says **In your
games**.

Searches look in the indexers' console categories and in Other (where older
systems' games are often filed). A release is passed over when it names another
system, is a set of many games (a romset, No-Intro or Redump pack), is something
else (a soundtrack, a PC or Switch game, a film), or is too big to be one game.
One that names the system comes first. **Choose a release** shows every release
found and why any was passed over, and grabs the one you pick. Wanted games are
looked for again by the hourly patrol, with everything else wanted.

Every download is checked before it goes in, the same way the games in your
folders are. A console game's file (loose, or inside a zip or 7z, even one with
other games in it) must have the hash No-Intro gives the version you want, or
another version of the same game; a MAME set must be named for the set and hold
at least one of its ROMs, and its parent, BIOS and device sets come in with it.
One that isn't the game is quarantined, its release blocked, and the next one
looked for. The game goes into its system's folder named as the list names it
(`snes/Super Metroid (Japan, USA) (En,Ja).sfc`, `mame/pacman.zip`), the system is
checked, and the game leaves **Wanted**.

Usenet indexers carry far fewer single retro games than films, so many searches
find nothing; Box Office keeps looking, less often after a few misses. A system
on **Do not touch** takes no new games.

## Box art

Pictures come from [libretro's thumbnails](https://thumbnails.libretro.com): the
box art, or for a game without, its title screen or a screenshot; a MAME clone
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
| `BOXOFFICE_LIBRETRO_DB_URL` | `https://raw.githubusercontent.com/libretro/libretro-database/master` | A copy of libretro's database, for No-Intro's lists and game facts |
| `BOXOFFICE_GAME_ART_URL` | libretro's thumbnails, then the same on GitHub | Where pictures are looked for, comma separated. `{system}` is libretro's name for the system, `{repo}` the same with `_` for spaces, `{kind}` `Named_Boxarts`, `Named_Titles` or `Named_Snaps`, and `{name}` the game |
