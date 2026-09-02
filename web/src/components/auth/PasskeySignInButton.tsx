import { useNavigate, useRouter } from '@tanstack/react-router'
import { Fingerprint } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { safeNext } from '#/lib/search'
import { authClient } from '#/server/auth/client'

export function PasskeySignInButton({ next }: { next: string }) {
  const router = useRouter()
  const navigate = useNavigate()

  async function handleClick() {
    const { error } = await authClient.signIn.passkey()
    if (error) {
      toast.error(m.auth_passkey_signin_error())
      return
    }
    await router.invalidate()
    // Re-validated here too (defence in depth) even though callers already sanitize `next` —
    // this component must never trust that upstream validation ran.
    await navigate({ href: safeNext(next) })
  }

  return (
    <Button
      type="button"
      variant="outline"
      className="w-full gap-2"
      onClick={() => void handleClick()}
    >
      <Fingerprint aria-hidden="true" />
      {m.auth_use_passkey()}
    </Button>
  )
}
