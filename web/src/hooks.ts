import { useQuery } from '@tanstack/react-query'
import { get, type Profile, type Quality, type RootFolder, type RootKind } from './api'

export const useProfiles = () => useQuery({ queryKey: ['profiles'], queryFn: () => get<Profile[]>('/profiles') })
export const useQualities = () => useQuery({ queryKey: ['qualities'], queryFn: () => get<Quality[]>('/qualities'), staleTime: Infinity })
export const useRootFolders = (kind: RootKind) =>
  useQuery({ queryKey: ['rootfolders', kind], queryFn: () => get<RootFolder[]>('/rootfolders?kind=' + kind) })

export function qualityName(qualities: Quality[] | undefined, id: number): string {
  return qualities?.find((q) => q.id === id)?.name ?? 'Unknown'
}
