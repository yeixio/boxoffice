import ts from 'typescript'
import { describe, expect, it } from 'vitest'

// Every word people read comes from the catalog, so a new page can't ship
// in English only. This finds text written straight into the markup:
// between tags, and in the attributes people read or hear.

// Every page and component's source, by path from src.
const sources = import.meta.glob<string>(['../**/*.tsx', '!../**/*.test.tsx'], { query: '?raw', import: 'default', eager: true })
const readAttributes = new Set(['title', 'aria-label', 'placeholder', 'alt', 'label'])

// Names that read the same in every language.
const allowed = new Set(['Box Office', 'Matinee', 'TMDB', 'TVDB', 'IMDb', 'Radarr', 'Sonarr', 'Lidarr', 'Readarr', 'NZBGet', 'SABnzbd', 'Deluge', 'qBittorrent', 'Transmission', 'Prowlarr', 'MusicBrainz', 'Open Library', 'Usenet', 'RSS', 'NZB', 'TLS', 'SQLite', 'PostgreSQL', 'Prometheus', 'Plex', 'Jellyfin', 'Emby'])

// An address, host name, path or region, such as http://matinee:7171,
// smtp.example.com, /mnt/nas or us-east-1, reads the same in every language too.
const machine = (s: string) => /^https?:\/\/\S+$/.test(s) || /^[\w@.:/-]+$/.test(s) && /[@./:-]/.test(s)
const readable = (s: string) => /\p{L}{2,}/u.test(s) && !allowed.has(s.trim()) && !machine(s.trim())

function hardcoded([path, text]: [string, string]): string[] {
  const src = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const found: string[] = []
  const at = (n: ts.Node) => `${path.slice(3)}:${src.getLineAndCharacterOfPosition(n.getStart()).line + 1}`
  const visit = (n: ts.Node) => {
    if (ts.isJsxText(n) && readable(n.text)) found.push(`${at(n)} ${n.text.trim()}`)
    if (ts.isJsxAttribute(n) && readAttributes.has(n.name.getText()) && n.initializer) {
      const init = n.initializer
      const value = ts.isStringLiteral(init)
        ? init.text
        : ts.isJsxExpression(init) && init.expression && (ts.isStringLiteral(init.expression) || ts.isNoSubstitutionTemplateLiteral(init.expression))
          ? init.expression.text
          : ''
      if (readable(value)) found.push(`${at(n)} ${n.name.getText()}="${value}"`)
    }
    ts.forEachChild(n, visit)
  }
  visit(src)
  return found
}

// Classes that put things on the left or right, which right-to-left languages
// need mirrored: ms-/me-/ps-/pe-/start-/end-/text-start do it by themselves.
// left-1/2 with -translate-x-1/2 centres, which reads the same both ways.
const physical = /(?<![\w-])-?(?:[a-z]+:)*(?:(?:ml|mr|pl|pr|left|right)-(?!1\/2)[\w./[\]]+|text-left|text-right|(?:rounded|border)-(?:[lr]|[tb][lr])(?:-[\w./[\]]+)?)(?=\s|$)/g
const quoted = /(["'`])((?:(?!\1).)*)\1/g

function sided([path, text]: [string, string]): string[] {
  return text.split('\n').flatMap((line, i) =>
    [...line.matchAll(quoted)].flatMap((q) => [...q[2].matchAll(physical)].map((m) => `${path.slice(3)}:${i + 1} ${m[0]}`)),
  )
}

describe('admin and request site text', () => {
  it('comes from the catalog', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(20)
    expect(Object.entries(sources).flatMap(hardcoded)).toEqual([])
  })

  it('lays out for right-to-left languages too', () => {
    expect(Object.entries(sources).flatMap(sided)).toEqual([])
  })
})
