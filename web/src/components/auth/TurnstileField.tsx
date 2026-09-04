import { Turnstile } from '@marsidev/react-turnstile'
import { useTurnstileSiteKey } from '#/lib/captcha'

/**
 * The Cloudflare Turnstile widget, or nothing at all when the deployment has no site key
 * (`useTurnstileSiteKey() === ''`): an empty sitekey makes Cloudflare's widget error out or never
 * load, and every consumer gates its submit on `useCaptchaEnabled()` from the same module, so the
 * two can never disagree about whether a token is expected.
 */
export function TurnstileField({ onToken }: { onToken: (token: string | null) => void }) {
  const siteKey = useTurnstileSiteKey()
  if (siteKey === '') return null

  return (
    <div data-slot="turnstile-field">
      <Turnstile
        siteKey={siteKey}
        onSuccess={onToken}
        onExpire={() => onToken(null)}
        onError={() => onToken(null)}
        // No `size` here on purpose: `@marsidev/react-turnstile` applies a fixed inline style to
        // this container based on `options.size` (e.g. `flexible` forces a 65px-tall, 300px-min
        // box) regardless of what the widget actually renders. Production's sitekey is configured
        // as an invisible widget that renders nothing, so that forced box used to sit as a dead
        // empty placeholder in the form. Leaving `size` unset means the container gets no inline
        // sizing at all — it collapses to nothing when the widget renders nothing, and still
        // sizes itself naturally around the dev/e2e test key's visible checkbox widget.
        options={{ theme: 'auto' }}
      />
    </div>
  )
}
