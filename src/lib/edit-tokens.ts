import { useSyncExternalStore } from 'react'

/**
 * Guest voters get an edit token instead of an account: the server hands one back when they add
 * themselves to a poll, and the browser keeps it so the same person can come back and change
 * their answer. It lives in `localStorage` under one key per poll, so a shared computer can hold
 * tokens for several polls without them colliding.
 *
 * Every function is best-effort: private mode, blocked storage and hand-edited values must never
 * throw into a render.
 */

export type EditToken = { participantId: string; token: string }

function storageKey(pollId: string): string {
  return `whenweall:edit:${pollId}`
}

function storage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function saveEditToken(pollId: string, participantId: string, token: string): void {
  try {
    storage()?.setItem(storageKey(pollId), JSON.stringify({ participantId, token }))
    notify()
  } catch {
    // Storage full or blocked: the visitor simply can't edit from another visit.
  }
}

export function loadEditToken(pollId: string): EditToken | null {
  try {
    const raw = storage()?.getItem(storageKey(pollId))
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return null
    const { participantId, token } = parsed as Partial<EditToken>
    if (typeof participantId !== 'string' || typeof token !== 'string') return null
    return { participantId, token }
  } catch {
    return null
  }
}

export function clearEditToken(pollId: string): void {
  try {
    storage()?.removeItem(storageKey(pollId))
    notify()
  } catch {
    // Nothing to do: the stale token only ever fails an authorization check.
  }
}

/**
 * Reading `localStorage` during render would disagree with the server, so the stored token is
 * exposed as an external store: `null` while the page renders on the server and during hydration,
 * then the real value. Snapshots are cached per poll so React sees a stable object between
 * renders, and `save`/`clear` notify subscribers so a vote updates the page without a reload.
 */
const listeners = new Set<() => void>()
const snapshots = new Map<string, { raw: string | null; value: EditToken | null }>()

function notify(): void {
  for (const listener of listeners) listener()
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange)
  if (typeof window !== 'undefined') window.addEventListener('storage', onChange)
  return () => {
    listeners.delete(onChange)
    if (typeof window !== 'undefined') window.removeEventListener('storage', onChange)
  }
}

function rawEntry(pollId: string): string | null {
  try {
    return storage()?.getItem(storageKey(pollId)) ?? null
  } catch {
    return null
  }
}

function snapshot(pollId: string): EditToken | null {
  const raw = rawEntry(pollId)
  const cached = snapshots.get(pollId)
  if (cached && cached.raw === raw) return cached.value
  const value = loadEditToken(pollId)
  snapshots.set(pollId, { raw, value })
  return value
}

function serverSnapshot(): EditToken | null {
  return null
}

/** The edit token this browser holds for a poll, or null. Safe to call during SSR. */
export function useEditToken(pollId: string): EditToken | null {
  return useSyncExternalStore(subscribe, () => snapshot(pollId), serverSnapshot)
}
