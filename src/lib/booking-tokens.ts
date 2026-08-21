import { useSyncExternalStore } from 'react'

/**
 * A visitor who books a slot gets a manage token back — the same secret that rides in the
 * `?t=` of the confirmation email's link. The browser keeps it so that coming back to
 * `/booking/<id>` later (a bookmark, the back button, a second tab) still shows the booking
 * without hunting for the email.
 *
 * One key per booking, so a shared computer can hold several. Every function is best-effort:
 * private mode, blocked storage and hand-edited values must never throw into a render.
 */

function storageKey(bookingId: string): string {
  return `samla:booking:${bookingId}`
}

function storage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function saveBookingToken(bookingId: string, token: string): void {
  try {
    storage()?.setItem(storageKey(bookingId), token)
    notify()
  } catch {
    // Storage full or blocked: the visitor still has the link in their confirmation email.
  }
}

export function loadBookingToken(bookingId: string): string | null {
  try {
    const raw = storage()?.getItem(storageKey(bookingId))
    return raw && raw.length > 0 ? raw : null
  } catch {
    return null
  }
}

export function clearBookingToken(bookingId: string): void {
  try {
    storage()?.removeItem(storageKey(bookingId))
    notify()
  } catch {
    // Nothing to do: a stale token only ever fails an authorization check.
  }
}

/**
 * Reading `localStorage` during render would disagree with the server, so the stored token is
 * exposed as an external store: `null` while the page renders on the server and during
 * hydration, then the real value. Snapshots are cached per booking so React sees a stable value
 * between renders, and `save`/`clear` notify subscribers.
 */
const listeners = new Set<() => void>()

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

function serverSnapshot(): string | null {
  return null
}

/** The manage token this browser holds for a booking, or null. Safe to call during SSR. */
export function useBookingToken(bookingId: string): string | null {
  return useSyncExternalStore(subscribe, () => loadBookingToken(bookingId), serverSnapshot)
}
