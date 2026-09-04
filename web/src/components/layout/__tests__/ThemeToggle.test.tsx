import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ThemeToggle } from '#/components/layout/ThemeToggle'

function mockMatchMedia(prefersDark: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: prefersDark && query.includes('prefers-color-scheme: dark'),
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  )
}

beforeEach(() => {
  mockMatchMedia(false)
  localStorage.clear()
  document.documentElement.className = ''
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('ThemeToggle', () => {
  it('renders a labelled button that starts in auto mode', () => {
    render(<ThemeToggle />)

    const button = screen.getByRole('button')
    expect(button).toBeInTheDocument()
    expect(button.getAttribute('aria-label')).toBeTruthy()
    expect(button).toHaveAttribute('data-theme', 'auto')
  })

  it('cycles auto -> light -> dark -> auto, updating <html> and localStorage', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)
    const button = screen.getByRole('button')

    await user.click(button)
    expect(button).toHaveAttribute('data-theme', 'light')
    expect(localStorage.getItem('theme')).toBe('light')
    expect(document.documentElement.classList.contains('light')).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    await user.click(button)
    expect(button).toHaveAttribute('data-theme', 'dark')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.classList.contains('light')).toBe(false)

    await user.click(button)
    expect(button).toHaveAttribute('data-theme', 'auto')
    expect(localStorage.getItem('theme')).toBe('auto')
    // auto resolves through matchMedia, which reports a light system theme here.
    expect(document.documentElement.classList.contains('light')).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('resolves auto against the system preference', async () => {
    mockMatchMedia(true)
    const user = userEvent.setup()
    render(<ThemeToggle />)
    const button = screen.getByRole('button')

    await user.click(button) // light
    await user.click(button) // dark
    await user.click(button) // auto
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('picks up a stored preference on mount', () => {
    localStorage.setItem('theme', 'dark')
    render(<ThemeToggle />)

    expect(screen.getByRole('button')).toHaveAttribute('data-theme', 'dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
