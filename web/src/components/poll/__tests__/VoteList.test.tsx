import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { VoteList } from '#/components/poll/VoteList'
import type { ViewerState } from '#/components/poll/viewer'
import type { Answer } from '#/lib/scoring'
import type { PollView } from '#/api/types'

afterEach(() => cleanup())

const OPTION_A = 'opt_a'
const OPTION_B = 'opt_b'

function makePoll(overrides: Partial<PollView> = {}): PollView {
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
    options: [
      {
        id: OPTION_A,
        position: 0,
        kind: 'datetime',
        startAt: '2026-09-01T16:30:00.000Z',
        endAt: '2026-09-01T18:00:00.000Z',
        label: null,
        capacity: null,
      },
      {
        id: OPTION_B,
        position: 1,
        kind: 'datetime',
        startAt: '2026-09-02T09:00:00.000Z',
        endAt: null,
        label: null,
        capacity: null,
      },
    ],
    participants: [
      {
        id: 'pa_1',
        name: 'Ada',
        userId: 'user_1',
        hasEmail: true,
        votes: { [OPTION_A]: 'yes', [OPTION_B]: 'no' },
        createdAt: '2026-08-01T11:00:00.000Z',
      },
      {
        id: 'pa_2',
        name: 'Iben',
        userId: null,
        hasEmail: false,
        votes: { [OPTION_A]: 'yes', [OPTION_B]: 'ifneedbe' },
        createdAt: '2026-08-01T12:00:00.000Z',
      },
    ],
    comments: [],
    scores: {
      [OPTION_A]: { yes: 2, ifneedbe: 0, no: 0, score: 4 },
      [OPTION_B]: { yes: 0, ifneedbe: 1, no: 1, score: 1 },
    },
    bestOptionId: OPTION_A,
    claims: {},
    ...overrides,
  }
}

function makeViewer(overrides: Partial<ViewerState> = {}): ViewerState {
  return {
    userId: null,
    participantId: null,
    editToken: null,
    isOwner: false,
    locale: 'en',
    timeZone: 'Europe/Oslo',
    ...overrides,
  }
}

function renderList({
  poll = makePoll(),
  viewer = makeViewer(),
  answers = {} as Record<string, Answer>,
  canAnswer = true,
}: {
  poll?: PollView
  viewer?: ViewerState
  answers?: Record<string, Answer>
  canAnswer?: boolean
} = {}) {
  const onAnswer = vi.fn()
  render(
    <VoteList
      poll={poll}
      viewer={viewer}
      answers={answers}
      onAnswer={onAnswer}
      canAnswer={canAnswer}
    />,
  )
  const row = (optionId: string) => screen.getByTestId(`vote-row-${optionId}`)
  return { onAnswer, row }
}

describe('VoteList', () => {
  it('renders one row per option, dated in the viewer timezone', () => {
    const { row } = renderList()

    expect(screen.getAllByTestId(/^vote-row-/)).toHaveLength(2)
    expect(within(row(OPTION_A)).getByText(/1 Sep/)).toBeInTheDocument()
    expect(within(row(OPTION_A)).getByText(/18:30/)).toBeInTheDocument()
  })

  it('re-reads the same option in another timezone', () => {
    const { row } = renderList({ viewer: makeViewer({ timeZone: 'UTC' }) })

    expect(within(row(OPTION_A)).getByText(/16:30/)).toBeInTheDocument()
  })

  it('crowns the leading option and only the leading option', () => {
    const { row } = renderList()

    expect(row(OPTION_A)).toHaveAttribute('data-best', 'true')
    expect(row(OPTION_B)).not.toHaveAttribute('data-best', 'true')
    expect(within(row(OPTION_A)).getByText(/best/i)).toBeVisible()
  })

  it('marks the finalized option instead of the leading one', () => {
    const { row } = renderList({
      poll: makePoll({ status: 'finalized', finalizedOptionId: OPTION_B }),
    })

    expect(row(OPTION_B)).toHaveAttribute('data-finalized', 'true')
    expect(row(OPTION_A)).not.toHaveAttribute('data-best', 'true')
  })

  it('carries the tally for each option', () => {
    const { row } = renderList({ canAnswer: false })

    expect(row(OPTION_A)).toHaveAttribute('data-yes', '2')
    expect(row(OPTION_A)).toHaveAttribute('data-ifneedbe', '0')
    expect(row(OPTION_B)).toHaveAttribute('data-yes', '0')
    expect(row(OPTION_B)).toHaveAttribute('data-ifneedbe', '1')
  })

  it('counts the unsaved answer of a visitor who has no row yet', () => {
    const { row } = renderList({ answers: { [OPTION_B]: 'yes' } })

    // Two saved participants plus the visitor about to become a third.
    expect(row(OPTION_B)).toHaveAttribute('data-yes', '1')
    expect(row(OPTION_B)).toHaveAttribute('data-total', '3')
  })

  it('swaps rather than double-counts the answer of someone editing their own row', () => {
    const { row } = renderList({
      viewer: makeViewer({ participantId: 'pa_2' }),
      answers: { [OPTION_A]: 'no' },
    })

    // Iben's saved "yes" on A is replaced by the draft "no", so only Ada's yes is left.
    expect(row(OPTION_A)).toHaveAttribute('data-yes', '1')
    expect(row(OPTION_A)).toHaveAttribute('data-total', '2')
  })

  it('reveals who said what when a row is opened', () => {
    const { row } = renderList()

    const toggle = within(row(OPTION_B)).getByRole('button', { name: /who said what/i })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(within(row(OPTION_B)).queryByText('Ada')).not.toBeInTheDocument()

    fireEvent.click(toggle)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const people = within(row(OPTION_B)).getByRole('list')
    expect(within(people).getByText('Ada')).toBeInTheDocument()
    expect(within(people).getByText('If need be')).toBeInTheDocument()
  })

  it('cycles the viewer answer forward on tap', () => {
    const { onAnswer, row } = renderList()

    fireEvent.click(within(row(OPTION_A)).getByRole('button', { name: /your answer/i }))

    expect(onAnswer).toHaveBeenCalledWith(OPTION_A, 'yes')
  })

  it('skips if-need-be when the poll does not allow it', () => {
    const poll = makePoll()
    poll.settings.allowIfNeedBe = false
    const { onAnswer, row } = renderList({ poll, answers: { [OPTION_A]: 'yes' } })

    fireEvent.click(within(row(OPTION_A)).getByRole('button', { name: /your answer/i }))

    expect(onAnswer).toHaveBeenCalledWith(OPTION_A, 'no')
  })

  it('offers no answer control once the poll is closed', () => {
    const { row } = renderList({ poll: makePoll({ status: 'closed' }), canAnswer: false })

    expect(within(row(OPTION_A)).queryByRole('button', { name: /your answer/i })).toBeNull()
  })

  it('shows the saved answer of a viewer who is not editing', () => {
    const { row } = renderList({
      poll: makePoll({ status: 'closed' }),
      viewer: makeViewer({ participantId: 'pa_2' }),
      canAnswer: false,
    })

    expect(within(row(OPTION_B)).getByTestId('your-answer')).toHaveAttribute(
      'data-answer',
      'ifneedbe',
    )
  })

  it('says so when nobody has answered yet', () => {
    renderList({ poll: makePoll({ participants: [], scores: {} }) })

    expect(screen.getByText(/no answers yet/i)).toBeInTheDocument()
  })
})
