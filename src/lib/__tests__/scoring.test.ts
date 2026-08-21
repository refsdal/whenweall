import { describe, expect, it } from 'vitest'
import { bestOptionId, nextAnswer, scoreOptions } from '#/lib/scoring'
describe('scoring', () => {
  const ids = ['a', 'b', 'c']
  it('scores yes=2, ifneedbe=1, no=0 and counts answers', () => {
    const s = scoreOptions(ids, [
      { optionId: 'a', answer: 'yes' },
      { optionId: 'a', answer: 'ifneedbe' },
      { optionId: 'b', answer: 'yes' },
      { optionId: 'b', answer: 'yes' },
      { optionId: 'c', answer: 'no' },
    ])
    expect(s.a).toEqual({ yes: 1, ifneedbe: 1, no: 0, score: 3 })
    expect(s.b).toEqual({ yes: 2, ifneedbe: 0, no: 0, score: 4 })
    expect(s.c).toEqual({ yes: 0, ifneedbe: 0, no: 1, score: 0 })
  })
  it('ignores votes for unknown options and gives zero rows for unvoted options', () => {
    const s = scoreOptions(ids, [{ optionId: 'zzz', answer: 'yes' }])
    expect(Object.keys(s)).toEqual(ids)
    expect(s.a.score).toBe(0)
  })
  it('best option is highest score, ties → earliest in order, null when nobody voted yes/ifneedbe', () => {
    expect(
      bestOptionId(
        ids,
        scoreOptions(ids, [
          { optionId: 'c', answer: 'yes' },
          { optionId: 'b', answer: 'yes' },
        ]),
      ),
    ).toBe('b')
    expect(bestOptionId(ids, scoreOptions(ids, [{ optionId: 'a', answer: 'no' }]))).toBeNull()
    expect(bestOptionId(ids, scoreOptions(ids, []))).toBeNull()
  })
  it('cycles answers', () => {
    expect(nextAnswer(null, true)).toBe('yes')
    expect(nextAnswer('yes', true)).toBe('ifneedbe')
    expect(nextAnswer('ifneedbe', true)).toBe('no')
    expect(nextAnswer('no', true)).toBeNull()
    expect(nextAnswer('yes', false)).toBe('no')
  })
})
