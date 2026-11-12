# Games

The **Games** section shows the video games you already have: arcade sets for
MAME, and console games such as the Super Nintendo, Game Boy and Mega Drive. Box
Office finds each system's folder, works out what every file is, checks whether
each game is complete, and shows them with box art, year, maker and players.

Box Office only reads your game files. It never moves, renames, rebuilds or
deletes one, and it doesn't download games. The one time it writes there is when
you import games yourself: **Import games** copies new ones into their system's
folder (see [Importing files](import.md)).

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
