import { describe, expect, it } from 'vitest'
import en from '../en.json'
import nb from '../nb.json'

function placeholders(value: string): Set<string> {
  const matches = value.matchAll(/\{(\w+)\}/g)
  return new Set(Array.from(matches, (m) => m[1] as string))
}

function messageKeys(messages: Record<string, string>): string[] {
  return Object.keys(messages).filter((key) => key !== '$schema')
}

describe('messages', () => {
  it('parses both locale files as objects', () => {
    expect(typeof en).toBe('object')
    expect(typeof nb).toBe('object')
  })

  it('has identical key sets across locales (excluding $schema)', () => {
    const enKeys = messageKeys(en).sort()
    const nbKeys = messageKeys(nb).sort()
    expect(nbKeys).toEqual(enKeys)
  })

  it('has matching placeholders for every key across locales', () => {
    for (const key of messageKeys(en)) {
      const enValue = (en as Record<string, string>)[key]
      const nbValue = (nb as Record<string, string>)[key]
      expect(nbValue, `nb.json is missing key "${key}"`).toBeDefined()

      const enPlaceholders = placeholders(enValue ?? '')
      const nbPlaceholders = placeholders(nbValue ?? '')

      for (const placeholder of enPlaceholders) {
        expect(
          nbPlaceholders.has(placeholder),
          `nb.json key "${key}" is missing placeholder "{${placeholder}}"`,
        ).toBe(true)
      }
    }
  })
})

/*
 * Marketing copy must not make promises the product might have to break. "Free forever" was the
 * specific one that prompted this: it commits the pricing model in public, and walking it back
 * later would make us look dishonest rather than merely changed. Stating what is true *now*
 * ("free and easy to use") carries the same benefit with none of the liability.
 *
 * Scoped to user-facing copy only — code comments are free to say "forever" about loop conditions.
 */
describe('no forward-looking promises in copy', () => {
  const FOREVER_CLAIMS = [
    /\bforever\b/i,
    /\balways free\b/i,
    /\bfree for life\b/i,
    /\bfor alltid\b/i,
    /\balltid gratis\b/i,
  ]

  it('never promises something is permanent', () => {
    for (const [locale, messages] of [
      ['en', en],
      ['nb', nb],
    ] as const) {
      for (const key of messageKeys(messages as Record<string, string>)) {
        const value = (messages as Record<string, string>)[key] ?? ''
        for (const claim of FOREVER_CLAIMS) {
          expect(
            claim.test(value),
            `${locale}.json key "${key}" promises permanence: "${value}"`,
          ).toBe(false)
        }
      }
    }
  })
})
