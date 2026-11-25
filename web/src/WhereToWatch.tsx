import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { ExternalLink } from 'lucide-react'
import { get, type DiscoverKind, type Provider, type WhereToWatch as Watch } from './api'
import { formatLocale } from './i18n'

// WhereToWatch says where a movie or show can be watched: the network a
// show is made for, and the streaming services, rentals and shops that
// have it, so someone can play it on a service they already pay for.
// Services differ by country; the country is the browser's until someone
// picks another, which is remembered on this device.

const countryKey = 'boxoffice.watchCountry'

// browserCountry is the country in the browser's languages, such as "US"
// from en-US, or "" when none says.
export function browserCountry(languages: readonly string[] = navigator.languages ?? [navigator.language]): string {
  for (const l of languages) {
    const m = /[-_]([A-Za-z]{2})(?:$|[-_])/.exec(l)
    if (m) return m[1].toUpperCase()
  }
  return ''
}

function savedCountry(): string {
  try {
    return localStorage.getItem(countryKey) ?? ''
  } catch {
    return ''
  }
}

function countryName(code: string): string {
  try {
    return new Intl.DisplayNames([formatLocale()], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

function Logos({ label, providers, link }: { label: string; providers?: Provider[]; link?: string }) {
  if (!providers?.length) return null
  return (
    <>
      <dt className="pt-2 text-sm text-muted">{label}</dt>
      <dd>
        <ul className="flex flex-wrap gap-2">
          {providers.map((p) => {
            const face = p.logo ? (
              <img src={p.logo} alt={p.name} loading="lazy" className="size-10 rounded-lg object-cover ring-1 ring-line" />
            ) : (
              <span className="flex h-10 items-center rounded-lg border border-line px-2 text-xs font-medium">{p.name}</span>
            )
            return (
              <li key={p.id} title={p.name}>
                {link ? (
                  <a href={link} target="_blank" rel="noreferrer" className="block rounded-lg transition hover:ring-2 hover:ring-marquee">
                    {face}
                  </a>
                ) : (
                  face
                )}
              </li>
            )
          })}
        </ul>
      </dd>
    </>
  )
}

export function WhereToWatch({ kind, tmdbId, onLeave, className = '' }: { kind: DiscoverKind; tmdbId: number; onLeave?: () => void; className?: string }) {
  const { t } = useTranslation()
  const [country, setCountry] = useState(() => savedCountry() || browserCountry())
  const watch = useQuery({
    queryKey: ['watch', kind, tmdbId, country],
    queryFn: () => get<Watch>(`/discover/watch/${kind}/${tmdbId}${country ? `?country=${country}` : ''}`),
    staleTime: 60 * 60_000,
    placeholderData: (prev) => prev,
  })
  const w = watch.data
  // Nothing known anywhere, or TMDB can't be asked: the page is no worse
  // without it.
  if (!w || (w.networks.length === 0 && w.countries.length === 0)) return null
  const offers = !!(w.stream?.length || w.free?.length || w.rent?.length || w.buy?.length)
  const choices = w.countries.includes(w.country) ? w.countries : [...w.countries, w.country].sort()
  const pick = (c: string) => {
    setCountry(c)
    try {
      localStorage.setItem(countryKey, c)
    } catch {
      // Not remembered; it still applies here.
    }
  }
  return (
    <section aria-labelledby={`watch-${kind}-${tmdbId}`} className={className}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h2 id={`watch-${kind}-${tmdbId}`} className="font-display text-base font-semibold">
          {t('watch.title')}
        </h2>
        {w.countries.length > 0 && (
          <select
            aria-label={t('watch.country')}
            value={w.country}
            onChange={(e) => pick(e.target.value)}
            className="max-w-48 rounded-md border border-line bg-paper px-2 py-1 text-xs"
          >
            {choices.map((c) => (
              <option key={c} value={c}>
                {countryName(c)}
              </option>
            ))}
          </select>
        )}
      </div>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-4 gap-y-2">
        {w.networks.length > 0 && (
          <>
            <dt className="pt-2 text-sm text-muted">{t('watch.network')}</dt>
            <dd>
              <ul className="flex flex-wrap gap-2">
                {w.networks.map((n) => (
                  <li key={n.id}>
                    <Link
                      to={`/discover/network-${n.id}`}
                      onClick={onLeave}
                      title={t('watch.moreFrom', { name: n.name })}
                      className="flex h-10 items-center justify-center rounded-lg border border-line bg-white px-3 transition hover:ring-2 hover:ring-marquee"
                    >
                      {n.logo ? <img src={n.logo} alt={n.name} loading="lazy" className="max-h-6 max-w-24 object-contain" /> : <span className="text-sm font-semibold text-black">{n.name}</span>}
                    </Link>
                  </li>
                ))}
              </ul>
            </dd>
          </>
        )}
        <Logos label={t('watch.stream')} providers={w.stream} link={w.link} />
        <Logos label={t('watch.free')} providers={w.free} link={w.link} />
        <Logos label={t('watch.rent')} providers={w.rent} link={w.link} />
        <Logos label={t('watch.buy')} providers={w.buy} link={w.link} />
      </dl>
      {!offers && w.countries.length > 0 && <p className="mt-1 text-sm text-muted">{t('watch.none', { country: countryName(w.country) })}</p>}
      {w.countries.length > 0 && (
        <p className="mt-2 text-xs text-muted">
          {w.link ? (
            <a href={w.link} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:text-ink">
              {t('watch.source')} <ExternalLink className="size-3" aria-hidden />
            </a>
          ) : (
            t('watch.source')
          )}
        </p>
      )}
    </section>
  )
}
