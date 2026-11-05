import { useQuery } from '@tanstack/react-query'
import { get, type Profile, type Quality, type RootFolder, type RootKind, type ShowDefaults } from './api'

export const useProfiles = () => useQuery({ queryKey: ['profiles'], queryFn: () => get<Profile[]>('/profiles') })
export const useQualities = () => useQuery({ queryKey: ['qualities'], queryFn: () => get<Quality[]>('/qualities'), staleTime: Infinity })
export const useRootFolders = (kind: RootKind) =>
  useQuery({ queryKey: ['rootfolders', kind], queryFn: () => get<RootFolder[]>('/rootfolders?kind=' + kind) })

export function qualityName(qualities: Quality[] | undefined, id: number): string {
  return qualities?.find((q) => q.id === id)?.name ?? 'Unknown'
}

export function useShowDefaults() {
  return useQuery({ queryKey: ['show-defaults'], queryFn: () => get<ShowDefaults>('/series/defaults') })
}

// monitorOptions are the choices for which of a new show's episodes to get.
export function monitorOptions(recent: number) {
  return [
    { value: 'all', label: 'Every season' },
    { value: 'recent', label: recent === 1 ? 'The latest aired season, and new ones' : `The last ${recent} seasons, and new ones` },
    { value: 'future', label: 'Only episodes not out yet' },
    { value: 'latest', label: 'Only the newest season' },
    { value: 'none', label: 'None for now' },
  ]
}

const profileKey = (kind: RootKind) => `boxoffice.profile.${kind}`

// pickProfile is the profile a new title gets unless someone picks another:
// the one last picked for that kind, else the first. Do not touch is only
// ever used when picked by hand: it's never the default.
export function pickProfile(profiles: Profile[] | undefined, kind: RootKind): Profile | undefined {
  const usable = (profiles ?? []).filter((p) => !p.locked && p.name !== 'Do not touch')
  let last = 0
  try {
    last = Number(localStorage.getItem(profileKey(kind))) || 0
  } catch {
    // storage blocked
  }
  return usable.find((p) => p.id === last) ?? usable[0]
}

// rememberProfile keeps the profile picked for a new title, for the next one.
export function rememberProfile(kind: RootKind, id: number) {
  try {
    localStorage.setItem(profileKey(kind), String(id))
  } catch {
    // storage blocked
  }
}
