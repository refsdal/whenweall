import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SlotCard, type SlotClaimant } from '#/components/signup/SlotCard'
import type { PollOptionView } from '#/server/polls/viewmodel'

afterEach(() => cleanup())

const OPTION_ID = 'opt_a'

const option: PollOptionView = {
  id: OPTION_ID,
  position: 0,
  kind: 'text',
  startAt: null,
  endAt: null,
  label: 'Bring the cake',
  capacity: 1,
}

function renderCard(overrides: Partial<React.ComponentProps<typeof SlotCard>> = {}): {
  onClaim: ReturnType<typeof vi.fn>
  onUnclaim: ReturnType<typeof vi.fn>
} {
  const onClaim = vi.fn()
  const onUnclaim = vi.fn()
  render(
    <SlotCard
      option={option}
      locale="en"
      timeZone="Europe/Oslo"
      count={0}
      capacity={1}
      claimants={[]}
      claimedByYou={false}
      disabledReason={null}
      pending={false}
      isOwner={false}
      onClaim={onClaim}
      onUnclaim={onUnclaim}
      {...overrides}
    />,
  )
  return { onClaim, onUnclaim }
}

const IBEN: SlotClaimant = { participantId: 'pa_2', name: 'Iben', isYou: false }
const YOU: SlotClaimant = { participantId: 'pa_1', name: 'Ada', isYou: true }

describe('SlotCard', () => {
  it('shows the slot label and an empty claimant list', () => {
    renderCard()

    const card = screen.getByTestId('slot-card')
    expect(card).toHaveAttribute('data-option-id', OPTION_ID)
    expect(within(card).getByText('Bring the cake')).toBeInTheDocument()
    expect(within(card).getByText(/be the first/i)).toBeInTheDocument()
  })

  it('claims a free slot', async () => {
    const user = userEvent.setup()
    const { onClaim } = renderCard()

    await user.click(screen.getByRole('button', { name: /claim/i }))

    expect(onClaim).toHaveBeenCalledWith(OPTION_ID)
  })

  it('marks a full slot and disables claiming', () => {
    renderCard({ count: 1, capacity: 1, claimants: [IBEN], disabledReason: 'full' })

    const card = screen.getByTestId('slot-card')
    expect(card).toHaveAttribute('data-full', 'true')
    expect(within(card).getByText('Full')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: /claim/i })).toBeDisabled()
  })

  it('offers to leave a slot you hold, even when it is full', async () => {
    const user = userEvent.setup()
    const { onUnclaim } = renderCard({
      count: 1,
      capacity: 1,
      claimants: [YOU],
      claimedByYou: true,
      disabledReason: 'full',
    })

    const leave = screen.getByRole('button', { name: /leave/i })
    expect(leave).toBeEnabled()
    await user.click(leave)

    expect(onUnclaim).toHaveBeenCalledWith(OPTION_ID, 'pa_1')
  })

  it('badges your own name in the claimant list', () => {
    renderCard({ count: 1, capacity: null, claimants: [YOU], claimedByYou: true })

    expect(within(screen.getByTestId('slot-card')).getByText(/^you$/i)).toBeInTheDocument()
  })

  it('lets the owner remove a claimant after confirming', async () => {
    const user = userEvent.setup()
    const { onUnclaim } = renderCard({
      count: 1,
      capacity: null,
      claimants: [IBEN],
      isOwner: true,
    })

    await user.click(screen.getByRole('button', { name: /remove iben/i }))
    await user.click(await screen.findByRole('button', { name: /^remove$/i }))

    expect(onUnclaim).toHaveBeenCalledWith(OPTION_ID, 'pa_2')
  })

  it('hides the remove control from people who are not the owner', () => {
    renderCard({ count: 1, capacity: null, claimants: [IBEN] })

    expect(screen.queryByRole('button', { name: /remove iben/i })).not.toBeInTheDocument()
  })

  it('disables claiming on a closed sheet', () => {
    renderCard({ disabledReason: 'closed' })

    expect(screen.getByRole('button', { name: /claim/i })).toBeDisabled()
  })
})
