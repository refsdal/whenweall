import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { VoteGrid } from '#/components/poll/VoteGrid'
import type { ViewerState } from '#/components/poll/viewer'
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
        endAt: null,
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

function renderGrid(poll = makePoll(), viewer = makeViewer()) {
  const onEditParticipant = vi.fn()
  const onRemoveParticipant = vi.fn()
  render(
    <VoteGrid
      poll={poll}
      viewer={viewer}
      onEditParticipant={onEditParticipant}
      onRemoveParticipant={onRemoveParticipant}
    />,
  )
  return { onEditParticipant, onRemoveParticipant }
}

describe('VoteGrid', () => {
  it('renders option headers in the viewer timezone', () => {
    renderGrid(makePoll(), makeViewer({ timeZone: 'Europe/Oslo' }))

    const header = screen.getByTestId(`option-header-${OPTION_A}`)
    expect(within(header).getByText('18:30')).toBeInTheDocument()
    expect(within(header).getByText(/1 Sep/)).toBeInTheDocument()
  })

  it('re-renders the same option in another timezone', () => {
    renderGrid(makePoll(), makeViewer({ timeZone: 'UTC' }))

    const header = screen.getByTestId(`option-header-${OPTION_A}`)
    expect(within(header).getByText('16:30')).toBeInTheDocument()
  })

  it('marks the best column and only the best column', () => {
    renderGrid()

    expect(screen.getByTestId(`option-header-${OPTION_A}`)).toHaveAttribute('data-best', 'true')
    expect(screen.getByTestId(`option-header-${OPTION_B}`)).not.toHaveAttribute('data-best', 'true')
    expect(within(screen.getByTestId(`option-header-${OPTION_A}`)).getByText(/best/i)).toBeVisible()
  })

  it('shows the tally per option', () => {
    renderGrid()

    const scoreA = screen.getByTestId(`score-${OPTION_A}`)
    expect(scoreA).toHaveAttribute('data-yes', '2')
    expect(scoreA).toHaveAttribute('data-ifneedbe', '0')
    expect(within(scoreA).getByText('2')).toBeInTheDocument()

    const scoreB = screen.getByTestId(`score-${OPTION_B}`)
    expect(scoreB).toHaveAttribute('data-yes', '0')
    expect(scoreB).toHaveAttribute('data-ifneedbe', '1')
  })

  it('lists participants with their answers as read-only cells', () => {
    renderGrid()

    expect(screen.getByText('Ada')).toBeInTheDocument()
    expect(screen.getByText('Iben')).toBeInTheDocument()
    expect(screen.queryAllByRole('button', { name: /answer/i })).toHaveLength(0)
    expect(screen.getAllByRole('img', { name: /yes/i }).length).toBeGreaterThan(0)
  })

  it('badges the viewer own row', () => {
    renderGrid(makePoll(), makeViewer({ participantId: 'pa_2' }))

    const row = screen.getByTestId('participant-row-pa_2')
    expect(within(row).getByText(/^you$/i)).toBeInTheDocument()
  })

  it('offers edit and remove on the viewer own row only', () => {
    const { onEditParticipant } = renderGrid(makePoll(), makeViewer({ participantId: 'pa_2' }))

    const own = screen.getByTestId('participant-row-pa_2')
    const editButton = within(own).getByRole('button', { name: /edit iben/i })
    editButton.click()
    expect(onEditParticipant).toHaveBeenCalledWith('pa_2')

    const other = screen.getByTestId('participant-row-pa_1')
    expect(within(other).queryByRole('button', { name: /edit/i })).not.toBeInTheDocument()
  })

  it('highlights the finalized column', () => {
    renderGrid(makePoll({ status: 'finalized', finalizedOptionId: OPTION_B }))

    expect(screen.getByTestId(`option-header-${OPTION_B}`)).toHaveAttribute(
      'data-finalized',
      'true',
    )
  })

  it('shows an empty state when nobody has answered', () => {
    renderGrid(makePoll({ participants: [] }))

    expect(screen.getByText(/no answers yet/i)).toBeInTheDocument()
  })
})
