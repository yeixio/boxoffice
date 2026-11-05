// Changing many titles at once: pick them on Movies or Shows, then set
// their quality profile (Do not touch, say) or whether they're watched for.

import { useState, type ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Lock } from 'lucide-react'
import { post } from './api'
import { count } from './format'
import { useProfiles } from './hooks'
import { Button, ErrorNote, Select } from './ui'

export function useSelection() {
  const [on, setOn] = useState(false)
  const [ids, setIds] = useState<Set<number>>(new Set())
  return {
    on,
    ids,
    start: () => setOn(true),
    stop: () => {
      setOn(false)
      setIds(new Set())
    },
    toggle: (id: number) =>
      setIds((s) => {
        const n = new Set(s)
        if (n.has(id)) n.delete(id)
        else n.add(id)
        return n
      }),
    set: (list: number[]) => setIds(new Set(list)),
  }
}

export type Selection = ReturnType<typeof useSelection>

// BulkBar sits at the bottom of the page while titles are being picked.
export function BulkBar({ kind, sel, shown }: { kind: 'movies' | 'series'; sel: Selection; shown: number[] }) {
  const qc = useQueryClient()
  const profiles = useProfiles()
  const locked = profiles.data?.find((p) => p.locked)
  const [profile, setProfile] = useState('')
  const [done, setDone] = useState('')
  const apply = useMutation({
    mutationFn: (body: { profileId?: number; monitored?: boolean }) => post<{ changed: number }>(`/${kind}/bulk`, { ids: [...sel.ids], ...body }),
    onSuccess: (r) => {
      setDone(`Changed ${count(r.changed, 'title', 'titles')}.`)
      qc.invalidateQueries({ queryKey: [kind === 'movies' ? 'movies' : 'series'] })
      sel.set([])
    },
  })
  const n = sel.ids.size
  const allShown = shown.length > 0 && shown.every((id) => sel.ids.has(id))
  return (
    <div className="sticky bottom-0 z-20 -mx-4 mt-6 border-t border-line bg-paper/95 px-4 py-3 backdrop-blur">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm">
        <span className="font-medium">{n ? `${count(n, 'title', 'titles')} picked` : 'Tap titles to pick them'}</span>
        <Button variant="ghost" onClick={() => sel.set(allShown ? [] : shown)}>
          {allShown ? 'Pick none' : `Pick all ${shown.length} shown`}
        </Button>
        <div className="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto">
          {locked && (
            <Button disabled={!n} busy={apply.isPending && apply.variables?.profileId === locked.id} onClick={() => apply.mutate({ profileId: locked.id })}>
              <Lock className="size-4" aria-hidden /> Do not touch
            </Button>
          )}
          <div className="w-44">
            <Select
              value={profile}
              onChange={setProfile}
              options={[{ value: '', label: 'Quality profile…' }, ...(profiles.data ?? []).filter((p) => !p.locked).map((p) => ({ value: p.id, label: p.name }))]}
            />
          </div>
          <Button disabled={!n || !profile} busy={apply.isPending && apply.variables?.profileId === Number(profile)} onClick={() => apply.mutate({ profileId: Number(profile) })}>
            Set profile
          </Button>
          <Button disabled={!n} onClick={() => apply.mutate({ monitored: true })}>
            Watch
          </Button>
          <Button disabled={!n} onClick={() => apply.mutate({ monitored: false })}>
            Pause
          </Button>
          <Button variant="ghost" onClick={sel.stop}>
            Done
          </Button>
        </div>
      </div>
      {done && !apply.isPending && <p className="mt-1 text-xs text-muted">{done}</p>}
      <ErrorNote error={apply.error} />
    </div>
  )
}

// Pickable wraps a poster tile: while picking, a tap picks it rather than
// opening the title.
export function Pickable({ sel, id, title, children }: { sel: Selection; id: number; title: string; children: ReactNode }) {
  const picked = sel.ids.has(id)
  return (
    <button type="button" onClick={() => sel.toggle(id)} aria-pressed={picked} aria-label={`Pick ${title}`} className="group relative block w-full text-left">
      <span
        className={`absolute left-2 top-2 z-10 flex size-6 items-center justify-center rounded-full border-2 text-xs font-bold ${picked ? 'border-marquee bg-marquee text-paper' : 'border-paper bg-ink/40 text-transparent'}`}
        aria-hidden
      >
        ✓
      </span>
      <span className={picked ? 'block opacity-100 [&_img]:ring-2 [&_img]:ring-marquee' : 'block opacity-80'}>{children}</span>
    </button>
  )
}

// LockedNote says what Do not touch means, on a title's page.
export function LockedNote() {
  return (
    <p className="mt-4 flex max-w-3xl items-start gap-2 rounded-md border border-line px-3 py-2 text-sm">
      <Lock className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
      <span>
        <span className="font-medium">Do not touch.</span> Box Office leaves this title's files exactly as they are: no searches, downloads, upgrades, replacing or deleting. Pick another
        quality profile below to change that.
      </span>
    </p>
  )
}
