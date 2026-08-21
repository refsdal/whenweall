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
