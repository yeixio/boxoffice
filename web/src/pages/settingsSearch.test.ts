import { describe, expect, it } from 'vitest'
import { matches, spans, words } from './settingsSearch'

describe('settings search', () => {
  it('needs every word, in any order and case', () => {
    expect(matches('News servers: host, port and connections', words('PORT host'))).toBe(true)
    expect(matches('News servers: host, port and connections', words('port quality'))).toBe(false)
    expect(matches('anything', words('   '))).toBe(true)
  })

  it('ignores accents', () => {
    expect(matches('Métadonnées et clé API', words('metadonnees cle'))).toBe(true)
    expect(matches('Dossiers racine', words('dossiérs'))).toBe(true)
  })

  it('finds text without spaces between words', () => {
    expect(matches('ダウンロードの設定', words('設定'))).toBe(true)
  })

  it('marks where each word is, in the text as written', () => {
    const text = 'Clé API et clé de TMDB'
    expect(spans(text, words('cle')).map(([a, b]) => text.slice(a, b))).toEqual(['Clé', 'clé'])
    expect(spans('Port', words('xyz'))).toEqual([])
  })

  it('keeps characters outside the basic plane whole', () => {
    const text = '🎬 Movie folders'
    expect(spans(text, words('movie')).map(([a, b]) => text.slice(a, b))).toEqual(['Movie'])
  })
})
