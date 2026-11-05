# Translations

Box Office's text in every language: the admin UI and the request site.

```text
i18n/
  languages.json          languages people can pick, with their direction and status
  i18n.go                 the list for the server, which checks a picked language
  locales/<language>/     one folder per BCP 47 tag, such as en, pt-BR or zh-Hant
    common.json           words both sites show: sizes, durations, sort orders, the language picker
    web.json              the admin UI's own text
    request.json          the request site's own text
```

English (`en`) is the source. Every other language has the same files and
keys. A key that is missing falls back to English, so a language can ship
before it is complete if `languages.json` marks it `partial`.

## Languages

The same as Matinee: English, German (`de`), Spanish (`es`), French (`fr`),
Italian (`it`), Brazilian Portuguese (`pt-BR`), Dutch (`nl`), Polish (`pl`),
Swedish (`sv`), Norwegian Bokmål (`nb`), Danish (`da`), Finnish (`fi`), Czech
(`cs`), Greek (`el`), Hungarian (`hu`), Romanian (`ro`), Turkish (`tr`),
Russian (`ru`), Ukrainian (`uk`), Indonesian (`id`), Malay (`ms`),
Vietnamese (`vi`), Thai (`th`), Hindi (`hi`), Japanese (`ja`), Korean
(`ko`), Simplified Chinese (`zh-Hans`), Traditional Chinese (`zh-Hant`),
Arabic (`ar`), Hebrew (`he`), Persian (`fa`) and Urdu (`ur`). Arabic,
Hebrew, Persian and Urdu are written right to left, and both sites mirror
their layout for them.

All but English are machine translated (`status: machine`). The language
picker says so, with a link here, until a person reviews one and its status
becomes `reviewed`.

Each admin picks a language in Settings (`PUT /api/v1/me/language`), and
each person on the request site picks one under Your account
(`PUT /api/portal/me/language`). It's kept with their account, so it follows
them to every browser. Until they pick one, each browser uses its own
languages: exactly (pt-BR), by base language (de-AT → de), by script (zh-TW
→ zh-Hant), or by another region (pt-PT → pt-BR), never Simplified for
Traditional Chinese. A language picked on a sign-in page is kept for the
account once they sign in.

Dates, numbers, sizes and relative times come from the browser's `Intl` in
the page's language, not from the catalog.

What the server writes itself, such as health checks, error messages, job
progress and Discover's row names, is still in English.

## Reviewing a translation

Read it in Box Office, fix wording in `locales/<tag>/*.json`, and set the
language's `status` to `reviewed` in `languages.json` in the same pull
request. Keep a term the same everywhere it appears, and keep the tone of
the English: short, plain, and talking to one person.

## Keys

- Keys are stable names, never the English text: `nav.settings`, not
  `"Settings"`. Changing the English wording keeps the key.
- Placeholders use `{{name}}` and must match English in every language. A
  plural form that stands for a single number, such as Arabic's `_two`, may
  spell the number out instead of using `{{count}}`.
- Plurals use i18next suffixes: `key_one` and `key_other` in English, and
  `_zero`, `_two`, `_few` or `_many` where a language needs them. Every form
  the language uses must be there; a test checks it.
- `<code>`, `<strong>`, `<b>`, `<link>`, `<path>` and the like are markup the
  page fills in. Keep them around the same words.
- Box Office, Matinee, TMDB, Radarr, Sonarr, NZBGet, Usenet, setting names
  from those apps (such as ControlPassword) and addresses stay as they are.

## Adding text

Text people read comes from the catalog: `t('section.key')` with
`useTranslation()`, or `<Trans>` when it has markup or a link inside.
`strings.test.ts` fails on text written straight into the markup, and on
`ml-`/`mr-`/`left-`/`text-left` classes, which don't mirror for right to
left (use `ms-`/`me-`/`start-`/`text-start`). `catalog.test.ts` fails on a
key the code uses that English doesn't have.

## Adding a language

1. Add it to `languages.json` with its name in itself, its direction, and
   `status: machine` or `partial`.
2. Copy `locales/en/` to `locales/<tag>/` and translate it.
3. Run the web tests. `catalog.test.ts` checks keys, placeholders and plural
   forms, and fails until every listed language has a folder.

Vitest caches the list of catalog files, so after adding a folder clear
`web/node_modules/.vite` and `web/node_modules/.vitest-cache` if the tests
don't see it.

In development the picker also offers two pseudo-languages: `en-XA`, which
stretches every string to find text that doesn't fit, and `ar-XB`, which
mirrors the layout to find what breaks right to left.
