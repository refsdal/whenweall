import { useEffect, useState } from 'react'

/**
 * A `Date` that re-renders on a timer. Used by the deadline countdown.
 *
 * The first value is read once, when the component mounts, so server and client agree on the
 * initial render; the interval only starts afterwards.
 */
export function useNow(intervalMs = 1000): Date {
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])

  return now
}
