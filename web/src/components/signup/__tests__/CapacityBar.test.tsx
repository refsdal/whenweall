import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { CapacityBar } from '#/components/signup/CapacityBar'

afterEach(() => cleanup())

describe('CapacityBar', () => {
  it('counts spots against a capacity', () => {
    render(<CapacityBar count={2} capacity={5} />)

    expect(screen.getByText('2 of 5 spots')).toBeInTheDocument()

    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '2')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
    expect(bar).toHaveAttribute('aria-valuemax', '5')
  })

  it('counts sign-ups without a bar when the slot is unlimited', () => {
    render(<CapacityBar count={3} capacity={null} />)

    expect(screen.getByText('3 signed up')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('says so when an unlimited slot is still empty', () => {
    render(<CapacityBar count={0} capacity={null} />)

    expect(screen.getByText('No one yet')).toBeInTheDocument()
  })

  it('marks a full slot', () => {
    render(<CapacityBar count={5} capacity={5} />)

    expect(screen.getByText('Full')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '5')
  })

  it('leaves a slot with room free of the full badge', () => {
    render(<CapacityBar count={4} capacity={5} />)

    expect(screen.queryByText('Full')).not.toBeInTheDocument()
  })
})
