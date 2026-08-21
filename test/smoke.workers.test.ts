import { env } from 'cloudflare:workers'
import { expect, it } from 'vitest'

it('runs in workerd with a D1 binding', async () => {
  const r = await env.DB.prepare('select 1 as one').first<{ one: number }>()
  expect(r?.one).toBe(1)
})
