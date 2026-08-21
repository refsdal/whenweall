export type Answer = 'yes' | 'ifneedbe' | 'no'

export type OptionScore = { yes: number; ifneedbe: number; no: number; score: number }

const POINTS: Record<Answer, number> = { yes: 2, ifneedbe: 1, no: 0 }

export function scoreOptions(
  optionIds: string[],
  votes: { optionId: string; answer: Answer }[],
): Record<string, OptionScore> {
  const result: Record<string, OptionScore> = {}
  for (const id of optionIds) {
    result[id] = { yes: 0, ifneedbe: 0, no: 0, score: 0 }
  }
  for (const vote of votes) {
    const entry = result[vote.optionId]
    if (!entry) continue
    entry[vote.answer] += 1
    entry.score += POINTS[vote.answer]
  }
  return result
}

export function bestOptionId(
  orderedOptionIds: string[],
  scores: Record<string, OptionScore>,
): string | null {
  let bestId: string | null = null
  let bestScore = 0
  for (const id of orderedOptionIds) {
    const score = scores[id]?.score ?? 0
    if (score > bestScore) {
      bestScore = score
      bestId = id
    }
  }
  return bestId
}

const CYCLE: (Answer | null)[] = [null, 'yes', 'ifneedbe', 'no']

export function nextAnswer(current: Answer | null, allowIfNeedBe: boolean): Answer | null {
  const cycle = allowIfNeedBe ? CYCLE : CYCLE.filter((a) => a !== 'ifneedbe')
  const index = cycle.indexOf(current)
  const nextIndex = (index + 1) % cycle.length
  return cycle[nextIndex] ?? null
}
