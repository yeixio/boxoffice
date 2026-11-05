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
