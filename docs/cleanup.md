# Clean-up rules

Clean-up rules make room in your library by picking out movies and shows that have been watched, or that nobody ever played. They need [Matinee](https://github.com/yeixio/matinee), because only the player knows what was watched.

A rule never surprises you:

- Adding a rule deletes nothing. It lists what it finds under **Leaving soon**, with the size of each title's folder.
- By default a rule **only lists**. You delete the titles you choose, or keep them.
- A rule deletes by itself only if you turn that on for it, and then only after a title has been listed for the days you set (at least 7), and at most 5 titles a day.

## What you need

- Matinee connected to Box Office (**Settings → Connect Matinee**), on a version that reports what was watched. It reports by itself, every few hours and when something changes. Nothing needs setting up.
- Box Office able to write to your library folders.

The Clean-up card says when Matinee last reported. If that was more than a day ago, rules wait and nothing is deleted.

## Adding a rule

1. Open **Settings → Library → Clean-up** and choose **Add a rule**.
2. Pick **Movies** or **Shows**.
3. Pick who has watched it:
   - **Everyone who started it has finished it.** Nobody is partway through.
   - **Someone has finished it.**
   - **Nobody has played it.**
4. Set how many days it must have been quiet. For watched titles that's counted from the last time anyone played it. For titles nobody has played it's counted from when the movie came, or the show's newest episode, and can't be less than 30.
5. For shows, **Only shows that have ended** is on, so shows with episodes still to come are left alone.
6. Set how many days a title stays on the Leaving soon list before it can go.
7. Leave **Delete by itself** off unless you want the rule to delete without asking.

As you fill it in, the box at the bottom shows what the rule would list right now and how much space that is. The preview changes nothing.

## Leaving soon

Each listed title shows its size, how many people played and finished it, and when it was last played.

- **Keep** takes it off the list, and no rule lists it again. Kept titles are shown under **Kept**, where you can let rules list them again.
- **Delete now** deletes the title's folder from the disk for good and removes the title from Box Office, after showing you the number of files and the size.
- Playing a title in Matinee takes it off the list the next time the rules run (every hour, or **Check now**).

## What a rule never touches

- Titles on the **Do not touch** quality profile.
- Titles you kept.
- Titles someone asked for in the last 30 days, or that have a request waiting.
- Titles whose folder Box Office can't delete safely: one that isn't inside its library folder, that holds another title, or that isn't there right now. If a network share drops out, nothing is deleted and nothing is removed from the list of titles.
- Anything that isn't what the list showed. If a title's folder has changed since it was listed (a new file, a different size), nothing is deleted, the list is brought up to date and that title's days start over.

A rule for titles **nobody has played** also waits if Matinee reports no plays at all, since that more likely means Matinee has just been set up than that nothing was ever watched.

## Good to know

- Deleting is for good. It doesn't go through the recycle folder.
- A deleted title is removed from Box Office, so it isn't downloaded again. A [list you follow](lists.md) won't add it back either.
- Changing a rule starts its Leaving soon list over.
- Matinee reports counts, not names: how many people played and finished each title. Box Office never learns who watched what.
- A show counts as finished by someone who has played every regular episode Matinee has of it. Specials don't count.
- Each deletion is noted on **Activity**.

## For other apps

Box Office, answering as Radarr and Sonarr, lists what's leaving at `GET /api/v3/boxoffice/leaving` (movies on `/radarr`, shows on `/sonarr`): `{"titles":[{"tmdbId":603,"title":"…","year":1999,"leavesAt":"…"}]}`. Its status lists `leaving` among `boxOfficeFeatures` when it has this.
