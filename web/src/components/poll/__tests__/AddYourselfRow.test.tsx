import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render } from '@testing-library/react'
import type { PollView } from '#/server/polls/viewmodel'

vi.mock('@tanstack/react-start', () => ({ useServerFn: () => vi.fn() }))
vi.mock('#/server/polls/participants.functions', () => ({
  addParticipant: vi.fn(),
  updateParticipant: vi.fn(),
}))

const { AddYourselfRow } = await import('#/components/poll/AddYourselfRow')
const { useAnswerDraft } = await import('#/components/poll/use-answer-draft')

afterEach(() => cleanup())

const OPTION_A = 'opt_a'
const OPTION_B = 'opt_b'
const OPTION_C = 'opt_c'

function makePoll(): PollView {
  return {
    id: 'abcdefghijkl',
    type: 'datetime',
    title: 'Dinner with the crew',
    description: null,
    location: null,
    timezone: 'Europe/Oslo',
    status: 'open',
    deadlineAt: null,
    finalizedOptionId: null,
    createdAt: '2026-08-01T10:00:00.000Z',
    settings: {
      requireParticipantEmail: false,
      allowComments: true,
      allowIfNeedBe: true,
      signupMaxClaims: 1,
    },
    notifications: null,
    owner: { name: 'Ada' },
    isOwner: false,
    options: [OPTION_A, OPTION_B, OPTION_C].map((id, position) => ({
      id,
      position,
      kind: 'datetime' as const,
      startAt: `2026-09-0${position + 1}T16:30:00.000Z`,
      endAt: null,
      label: null,
    })),
    participants: [],
    comments: [],
    scores: {},
    bestOptionId: null,
  } as unknown as PollView
}

const SESSION = { user: { id: 'usr_1', name: 'Iben', email: 'iben@example.com' } }

/** The row is presentational now — the draft it paints into lives a level up, in `PollPage`. */
function Harness({ poll }: { poll: PollView }) {
  const draft = useAnswerDraft({ poll, session: SESSION as never, onSaved: () => {} })
  return (
    <table>
      <tbody>
        <AddYourselfRow
          poll={poll}
          optionLabels={{ [OPTION_A]: 'A', [OPTION_B]: 'B', [OPTION_C]: 'C' }}
          draft={draft}
        />
      </tbody>
    </table>
  )
}

function renderRow() {
  const poll = makePoll()
  const { container } = render(<Harness poll={poll} />)

  const cell = (optionId: string) => {
    const td = container.querySelector(`td[data-option-id="${optionId}"]`)
    if (td === null) throw new Error(`no cell for ${optionId}`)
    return td
  }
  const answerOf = (optionId: string) =>
    cell(optionId).querySelector('[data-answer]')?.getAttribute('data-answer')

  return { cell, answerOf }
}

describe('AddYourselfRow drag-to-paint', () => {
  it('gives every cell dragged over the answer the first cell was headed for', () => {
    const { cell, answerOf } = renderRow()

    // The press picks "yes" (a blank cell's next answer); the origin cell is set by its own click.
    fireEvent.pointerDown(cell(OPTION_A), { pointerType: 'mouse', button: 0 })
    fireEvent.click(cell(OPTION_A).querySelector('button') as Element)
    fireEvent.pointerEnter(cell(OPTION_B), { pointerType: 'mouse' })
    fireEvent.pointerEnter(cell(OPTION_C), { pointerType: 'mouse' })

    expect(answerOf(OPTION_A)).toBe('yes')
    expect(answerOf(OPTION_B)).toBe('yes')
    expect(answerOf(OPTION_C)).toBe('yes')
  })

  it('stops painting once the pointer is released', () => {
    const { cell, answerOf } = renderRow()

    fireEvent.pointerDown(cell(OPTION_A), { pointerType: 'mouse', button: 0 })
    fireEvent.pointerUp(window)
    fireEvent.pointerEnter(cell(OPTION_B), { pointerType: 'mouse' })

    expect(answerOf(OPTION_B)).toBe('none')
  })

  it('leaves touch alone, so a drag across the grid still scrolls the page', () => {
    const { cell, answerOf } = renderRow()

    fireEvent.pointerDown(cell(OPTION_A), { pointerType: 'touch', button: 0 })
    fireEvent.pointerEnter(cell(OPTION_B), { pointerType: 'touch' })

    expect(answerOf(OPTION_B)).toBe('none')
  })

  it('paints the answer the origin cell was headed for, not a fixed one', () => {
    const { cell, answerOf } = renderRow()

    // Cycle A up to "yes" first, so the next press aims at "if need be".
    fireEvent.click(cell(OPTION_A).querySelector('button') as Element)
    expect(answerOf(OPTION_A)).toBe('yes')

    fireEvent.pointerDown(cell(OPTION_A), { pointerType: 'mouse', button: 0 })
    fireEvent.pointerEnter(cell(OPTION_B), { pointerType: 'mouse' })

    expect(answerOf(OPTION_B)).toBe('ifneedbe')
  })
})
