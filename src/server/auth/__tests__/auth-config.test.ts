import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const read = (rel: string) => readFileSync(path.join(import.meta.dirname, '..', rel), 'utf8')

describe('auth config drift guard', () => {
  it('keeps the runtime and CLI plugin lists in sync (passkey + locale)', () => {
    const runtime = read('auth.ts')
    const cli = read('auth.cli.ts')
    for (const source of [runtime, cli]) {
      expect(source).toContain('passkey(')
      expect(source).toContain('locale')
    }
  })

  it('keeps the runtime and CLI additionalFields in sync for handle (v3 booking pages)', () => {
    const runtime = read('auth.ts')
    const cli = read('auth.cli.ts')
    for (const source of [runtime, cli]) {
      expect(source).toContain('handle')
      expect(source).toMatch(/handle:\s*{\s*type:\s*'string'/)
    }
  })
})
