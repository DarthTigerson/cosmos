import { useSyncExternalStore } from 'react'

// The notification center's state, shared by the bell, the peek above it
// and the panel. Two kinds of thing land here:
//
// - notes: conditions that hold until they clear (proposals to review, a
//   model that failed, messages from her not yet read). The footer works
//   them out each render and passes their ids in with sync().
// - blips: one-off moments (she fell asleep, she woke). They only peek
//   for a few seconds and are never listed in the panel.
//
// A note is "seen" once the panel closes while it shows, which quiets the
// bell; "cleared" once its × is pressed, which takes it out of the panel
// too. Both are forgotten when the condition goes away, so if it comes back
// it is announced again.

export type BlipKind = 'asleep' | 'awake' | 'problem' | 'info'

export interface Blip {
  id: string
  seq: number
  at: number
  kind: BlipKind
  text: string
}

export interface Arrival {
  ids: string[]
  at: number
  seq: number
}

interface NotesState {
  open: boolean
  seen: string[]
  cleared: string[]
  known: string[]
  arrival: Arrival | null
  blips: Blip[]
}

let state: NotesState = { open: false, seen: [], cleared: [], known: [], arrival: null, blips: [] }
const listeners = new Set<() => void>()

function update(patch: Partial<NotesState>) {
  state = { ...state, ...patch }
  listeners.forEach((l) => l())
}

let blipSeq = 0

export const notes = {
  toggle: () => update({ open: !state.open }),
  close: () => state.open && update({ open: false }),
  see: (ids: string[]) => {
    const next = [...new Set([...state.seen, ...ids])]
    if (next.length !== state.seen.length) update({ seen: next })
  },
  clear: (ids: string[]) =>
    update({ seen: [...new Set([...state.seen, ...ids])], cleared: [...new Set([...state.cleared, ...ids])] }),
  // sync takes the ids of every note that holds right now. One that was
  // not there last time has arrived: the bell rings and the peek shows it.
  sync: (active: string[]) => {
    const live = new Set(active)
    const known = new Set(state.known)
    const fresh = active.filter((id) => !known.has(id))
    const seen = state.seen.filter((id) => live.has(id))
    const cleared = state.cleared.filter((id) => live.has(id))
    if (fresh.length === 0 && active.length === state.known.length && seen.length === state.seen.length && cleared.length === state.cleared.length) return
    update({
      known: active,
      seen,
      cleared,
      ...(fresh.length > 0 ? { arrival: { ids: fresh, at: Date.now(), seq: (state.arrival?.seq ?? 0) + 1 } } : {}),
    })
  },
  blip: (kind: BlipKind, text: string) => {
    blipSeq++
    update({ blips: [...state.blips, { id: `blip-${blipSeq}`, seq: blipSeq, at: Date.now(), kind, text }] })
  },
  drop: (id: string) => update({ blips: state.blips.filter((b) => b.id !== id) }),
  dropAll: () => state.blips.length > 0 && update({ blips: [] }),
}

export function useNotes(): NotesState {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => state,
  )
}
