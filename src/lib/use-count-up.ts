import { useEffect, useRef, useState } from 'react'

const DEFAULT_MS = 600

/**
 * Counts from the previous value to the next one so a number that changes while someone is reading
 * draws the eye.
 *
 * Snaps instantly when the caller asks it not to animate, and on the very first render — a
 * server-rendered number must not animate up from zero on hydration.
 */
export function useCountUp(value: number, animate: boolean, durationMs = DEFAULT_MS): number {
  const [display, setDisplay] = useState(value)
  const previous = useRef(value)
  const firstRender = useRef(true)

  useEffect(() => {
    const from = previous.current
    previous.current = value

    if (firstRender.current) {
      firstRender.current = false
      setDisplay(value)
      return
    }
    if (!animate || from === value) {
      setDisplay(value)
      return
    }

    let raf = 0
    const started = performance.now()
    const tick = (now: number) => {
      const progress = Math.min(1, (now - started) / durationMs)
      // easeOutQuad: fast to start, settles gently on the final number.
      const eased = 1 - (1 - progress) * (1 - progress)
      setDisplay(Math.round(from + (value - from) * eased))
      if (progress < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [value, animate, durationMs])

  return display
}
