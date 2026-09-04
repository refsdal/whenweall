import { KeyRound } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { safeNext } from '#/lib/search'
import { oauthAuthorizeUrl } from '#/api/auth'

/**
 * The bring-your-own-OIDC counterpart of `GoogleButton`: `provider` is `publicConfig.oidcName`
 * (the Go side mounts `/api/v1/auth/oauth/<OIDC_NAME>/authorize` — internal/auth/routes.txt),
 * `name` is the same value as the human-readable label. Only rendered when
 * `publicConfig.oidcEnabled` is true.
 */
export function OidcButton({ provider, name, next }: { provider: string; name: string; next: string }) {
  async function handleClick() {
    try {
      const url = await oauthAuthorizeUrl(provider, new URL(safeNext(next), window.location.origin).toString())
      window.location.href = url
    } catch (error) {
      toast.error(authErrorMessage(error))
    }
  }

  return (
    <Button type="button" variant="outline" className="w-full gap-2" onClick={() => void handleClick()}>
      <KeyRound aria-hidden="true" className="size-4" />
      {m.auth_continue_with_sso({ name })}
    </Button>
  )
}
