# UI audit against the navigation and settings standard (November 2026)

This is the audit asked for in #196. It checks the web UI, as of commit 093a5a0, against [Navigation & Settings Design Principles](navigation-and-settings.md), section by section. It was done on a demo server at three widths: desktop (1440 px, full sidebar), tablet (900 px, icons-only rail) and phone (390 px, header menu).

Each section says what complies, what doesn't, and the fix. Every fix below is **done**. The small ones landed first; the ones that moved pages or settings waited until Mike agreed the proposed structure on 2026-11-25, and landed in the same pull request (#209). The structure as built is at the end.

## What the UI has today

**Sidebar** (desktop, and the phone menu in the same order):

| Group | Items |
| --- | --- |
| Library | Discover, Movies, Shows, Music\*, Books\*, Audiobooks\*, Games\*, Photos and videos\*, Coming up, Import files |
| Status | Requests, Downloads, Activity, Health |
| Setup | Move in (until moving in is finished), Backups, Settings |
| Footer | Sign out *name*, Icons only, version |

\* only when that kind of media is turned on.

**Settings tabs:** General, Library, Downloads, Searching, Apps and devices, plus search-only "On other pages" cards.

| Tab | Cards |
| --- | --- |
| General | Language, Time zone, TMDB, Your password, Passkeys, One-time code, Single sign-on, Phone app and notifications, Restart Box Office, Database, then a line with version, OS and data folder |
| Library | More than movies and TV, root folders for each kind, Move titles, New shows, Rules for new titles, Lists, Problem files, Quality check, Subtitles, Clean-up, Recycle folders |
| Downloads | Downloading, Speed schedule, Download order, News servers, Download apps |
| Searching | Indexers, Quality profiles, Automatic searches, Data cap |
| Apps and devices | Connect Matinee, Send to e-readers, Move in |

**Settings on other pages:** alert rules and where alerts go (Health → Alerts), backup schedule and places (Backups), the request site and its members (Requests), music quality (Music), per-title options (each title's page), per-folder Scan and Move titles (root folders).

## Findings by section

### 2.1 Information architecture

- Complies: most settings have one place, and per-resource settings live on the resource (3.4).
- **Doesn't:** General mixes four things with different scopes: personal settings (language, password, passkeys, one-time code), app-wide behaviour (time zone), an external service (TMDB), and server maintenance (restart, database move). Nobody would look for "Restart" next to "Language".
- **Doesn't:** notifications are split three ways: alert rules and email/webhooks on Health → Alerts, push to phones on Settings → General, and the alert history on Health.
- **Doesn't:** "Import files" sits in Library among things to browse, but it's an action that brings files in, like a download.
- **Doesn't:** Backups is a page in the sidebar, though it is mostly configuration (schedule, places, passphrase) plus two actions.
- Fix (**done**): the structure at the end.

### 2.2 Hick's Law

- With every kind of media on, the sidebar has 18 destinations; with none extra, 12. The groups keep it scannable, but Setup holds things people open rarely (Move in once, Backups now and then).
- Fix (**done**): Import files moves into Downloads, Backups moves into Settings, Move in stays only until moving in is done (it already leaves then) and is also reachable from Settings → Connections. Settings and the account move to the sidebar's footer. That leaves 8 primary destinations (Discover, Movies, Shows, Coming up, Requests, Downloads, Activity, Health) plus one per kind of media turned on.

### 2.3 Jakob's Law

| Expected | Today | Verdict |
| --- | --- | --- |
| User profile / account | Spread over Settings → General; sign out in the sidebar footer | **Doesn't comply**: no account place |
| Theme | Follows the device's light/dark setting; no choice | Complies (no setting needed), noted as a possible Appearance card later |
| Notifications | Health → Alerts and Settings → General | **Doesn't comply** |
| Password and authentication | Settings → General | Partly: personal sign-in ways and single sign-on for everyone are mixed |
| API keys | Settings → Apps and devices → Connect Matinee | Complies (Connections) |
| Version / About | Sidebar footer and a line under General | **Partly**: no About section with licenses and docs |
| Data export | Backups page (download a backup) | Complies once Backups is under System |
| External services | TMDB in General, Matinee and e-readers in Apps and devices | **Partly**: TMDB should join the others |

### 2.4 Progressive disclosure

- Complies: rarely used fields sit inside dialogs (servers, indexers, profiles), and OpenSubtitles hides behind an expander.
- **Doesn't:** the Database card shows a PostgreSQL connection form on General to everyone, though most people never move. It belongs in System, which most people never open.
- Fix (**done**): System tab.

### 2.5 Navigation vs configuration

- **Doesn't:** the Setup group is configuration (Backups, Settings) next to a one-off workflow (Move in). Health → Alerts puts configuration inside a status page.
- Fix (**done**): Setup group goes; Alerts settings move to Settings → Notifications; Health keeps the check-up, server, log and the list of recent alerts.

### 3.1 Primary navigation

Complies apart from the items above.

### 3.2 Application settings

Mapping today's tabs to the recommended categories:

| Recommended | Box Office today | Proposed tab |
| --- | --- | --- |
| General | General (mixed) | General: time zone, kinds of media |
| Appearance | none (follows the device) | not needed yet |
| Notifications | Health → Alerts, Phone app card | Notifications |
| Privacy & Security | Single sign-on in General | System → Sign-in for everyone |
| Connections | Apps and devices, TMDB in General | Connections |
| Advanced | Restart, Database in General | System |
| About | version line under General | System → About |

Library, Downloads and Searching stay as they are: they are Box Office's own domains and people coming from Sonarr/Radarr look for them by those names.

### 3.3 Account settings

- **Doesn't:** Box Office has several admins with single sign-on and passkeys, but personal settings sit among app settings, and there is no place that says "this is you".
- Fix (**done**): a "Your account" tab first in Settings with language, password, passkeys and one-time code; the sidebar footer shows the signed-in name, opening a small menu with Your account and Sign out.

### 3.4 Contextual settings

Complies: title options on title pages, Scan and Move titles on root folders, music quality on Music, the request site on Requests, profiles edited where they are listed. Global Settings links to Problem files rather than repeating it.

### 4. Classification

Done: every setting now has a scope and complexity in [settings-inventory.md](settings-inventory.md).

### 5.1 Naming

- Complies: no "Other" or "Miscellaneous". "Apps and devices" is fine, but "Connections" is the conventional name and also fits TMDB.
- "On other pages" is a search-only group, never shown as a tab, so it's acceptable.

### 5.2 Depth

Complies: at most Settings → tab → card → dialog. No one-setting tabs today, and the proposal avoids them (Security was folded into System for that reason).

### 5.3 Visual consistency

- Complies: one icon set (Lucide), the same selected state (dark fill) in sidebar, rail and phone menu, the same tab style in Settings and Health on desktop.
- **Didn't:** on the tablet rail (768–1023 px, or "Icons only"), icons stand alone and the name only appears as a mouse tooltip after a delay, never on keyboard focus. Truck (Move in), inbox (Requests) and the database drum (Backups) aren't universally understood.
- Fix (**done**): rail items show their name in a label beside the icon on hover and on keyboard focus, straight away.

### 5.4 Responsive

Complies: sidebar on desktop, rail on tablet, folding menu with group headings on phones, Settings tabs as wrapping pills on phones.

### 6.1–6.3 Settings presentation

- Complies: cards group related controls, most complex settings explain what they do and when they take effect, defaults work without setup.
- Partly: few cards show which values are defaults or offer a reset. Not worth a sweeping change; the inventory records each default so cards can add "Default" hints when touched.

### 6.4 Destructive settings

- Complies: deleting files, removing indexers/servers/clients, empty recycle folders, clean-up rules and the database move all confirm with the consequences spelled out.
- **Didn't:** Restart and the database move sat among ordinary preferences on General with the same look.
- Fix (**done** for the look): both cards now have a red edge, the colour of the danger buttons, so they stand apart from the preferences around them. Moving them to System is **done**.

### 7. Platform standards

Complies: the phone app install follows each browser's own flow; tabs, menus and switches use native-like patterns. Tabs didn't follow the WAI-ARIA keyboard pattern (see 8).

### 8. Accessibility

- Complies: skip link, visible focus ring everywhere, accessible names on icon buttons, badges carry text labels, state not by colour alone (switch position + label), dark mode follows the device.
- **Didn't:** tab rows (Settings, Health, Downloads, and others using `TabRow`) only moved with Tab; the WAI-ARIA pattern is arrow keys, Home and End, with one tab stop for the row.
- Fix (**done**): arrow keys, Home and End move between tabs and select them; only the selected tab is in the Tab order.
- **Didn't:** animations and transitions ignored the device's "reduce motion" setting.
- Fix (**done**): with reduce motion on, transitions and animations are cut to near zero and smooth scrolling is off.

### 9. Feature placement

Done: the pull request template asks the placement questions and the review checklist for any change that adds a page, menu entry or setting.

### 10. Settings inventory

Done: [settings-inventory.md](settings-inventory.md), linked from the standard.

### 11. Review checklist

Run over the structure below: every item passes, with one note: "Sign-in for everyone" (single sign-on) sits in System rather than its own Security tab, because a Security tab would hold one card (5.2).

## Structure (agreed and built)

**Sidebar**

| Group | Items |
| --- | --- |
| Library | Discover, Movies, Shows, then each kind of media turned on, Coming up |
| Status | Requests, Downloads, Activity, Health |
| Setup (only until moved in) | Move in |
| Footer | Settings, *your name* (opens Your account) with a sign-out button beside it, Icons only, version |

Import files becomes a button on Downloads (beside Add NZB) and keeps its address, `/import-files`. Backups keeps its address, `/backups`, which opens Settings → System.

**Settings tabs**

| Tab | Cards |
| --- | --- |
| General | Time zone, More than movies and TV |
| Library | as before, less "More than movies and TV" |
| Downloads | as before |
| Searching | as before |
| Notifications | Phone app and notifications, Alerts (rules, email, webhooks, Prometheus) |
| Connections | Connect Matinee, TMDB, Send to e-readers, Move in |
| System | Backups, Single sign-on, About, Restart, Database |
| Your account | Language, Your password, Passkeys, One-time code |

Your account is the last tab rather than the first: General stays the tab Settings opens on, and the account is also reached from the sidebar's foot. Health → Alerts keeps what's wrong now and the alerts sent, with a link to change them. The request site's settings stay on Requests (section 3.4) and Settings search finds them.

Every card keeps its id, so `/settings#<id>` links and the Settings search keep working; `/settings?tab=apps` opens Connections and `/backups` opens Settings → System → Backups. New and renamed labels are translated in all 32 languages.
