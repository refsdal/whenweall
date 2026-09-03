import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useCaptchaEnabled, useTurnstileSiteKey } from '#/lib/captcha'

// Outside a RouterProvider there is no root context, hence no publicConfig, hence no site key —
// the same state a deployment without TURNSTILE_* is in. (The "enabled" branch is exercised by
// the component tests, which mock this module.)
describe('captcha capability hook', () => {
  it('reports captcha disabled and an empty site key when no config is reachable', () => {
    expect(renderHook(() => useTurnstileSiteKey()).result.current).toBe('')
    expect(renderHook(() => useCaptchaEnabled()).result.current).toBe(false)
  })
})
