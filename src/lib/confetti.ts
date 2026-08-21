import confetti from 'canvas-confetti'

/**
 * Brand colours as hex, because canvas-confetti parses hex/rgb only (the tokens in
 * `styles.css` are oklch). Keep in sync with `--primary`, `--yes` and `--ifneedbe`.
 */
const COLORS = ['#f3562e', '#ff8f68', '#3fc168', '#f3ba25', '#ffe6dd']

function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return true
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

/**
 * Fires a celebratory confetti burst.
 *
 * - `finalize` — two bursts angled in from both edges, for "the time is set".
 * - `vote` — one small burst from the bottom centre, for "your vote landed".
 *
 * No-ops during SSR and whenever the visitor prefers reduced motion.
 */
export function celebrate(kind: 'finalize' | 'vote'): void {
  if (prefersReducedMotion()) return

  if (kind === 'vote') {
    void confetti({
      particleCount: 40,
      spread: 55,
      startVelocity: 32,
      scalar: 0.8,
      ticks: 120,
      origin: { x: 0.5, y: 0.9 },
      colors: COLORS,
      disableForReducedMotion: true,
    })
    return
  }

  const base = {
    particleCount: 90,
    spread: 70,
    startVelocity: 48,
    ticks: 220,
    colors: COLORS,
    disableForReducedMotion: true,
  }

  void confetti({ ...base, angle: 60, origin: { x: 0, y: 0.75 } })
  void confetti({ ...base, angle: 120, origin: { x: 1, y: 0.75 } })
}
