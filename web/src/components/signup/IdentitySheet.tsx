import { useState } from 'react'
import * as z from 'zod'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { FormError, nextFailure, type FormFailure } from '#/components/ui/form-error'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'

export type ClaimIdentityValues = {
  name: string
  email: string | undefined
  turnstileToken: string | undefined
}

/**
 * Asked once, the first time someone takes a slot: who are you, and (if the organiser wants it)
 * where can we reach you. Everything after that is a single click, because the claim comes back
 * with an edit token this browser keeps.
 *
 * Validation is inline rather than a toast: the field that needs fixing is right there, and a
 * dialog steals focus from the toast region anyway.
 */
export function IdentitySheet({
  open,
  onOpenChange,
  requireEmail,
  needsCaptcha,
  defaultName = '',
  slotLabel,
  submitting = false,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  requireEmail: boolean
  needsCaptcha: boolean
  defaultName?: string
  slotLabel?: string
  submitting?: boolean
  onSubmit: (values: ClaimIdentityValues) => void | Promise<void>
}) {
  const [name, setName] = useState(defaultName)
  const [email, setEmail] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [error, setError] = useState<FormFailure | null>(null)

  function submit() {
    const trimmedName = name.trim()
    if (!trimmedName) {
      setError((current) => nextFailure(current, m.poll_error_name_required()))
      return
    }
    const trimmedEmail = email.trim()
    if (requireEmail && !trimmedEmail) {
      setError((current) => nextFailure(current, m.poll_error_email_required()))
      return
    }
    if (trimmedEmail && !z.email().safeParse(trimmedEmail).success) {
      setError((current) => nextFailure(current, m.poll_error_email_invalid()))
      return
    }
    if (needsCaptcha && !captchaToken) {
      setError((current) => nextFailure(current, m.poll_error_captcha()))
      return
    }

    setError(null)
    void onSubmit({
      name: trimmedName,
      email: trimmedEmail || undefined,
      turnstileToken: captchaToken ?? undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{m.signup_identity_title()}</DialogTitle>
          <DialogDescription>{slotLabel ?? m.signup_identity_body()}</DialogDescription>
        </DialogHeader>

        <form
          // Validation is ours: the browser's bubbles are unlocalised and land outside the dialog.
          noValidate
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="signup-identity-name">{m.poll_your_name_label()}</Label>
            <Input
              id="signup-identity-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={80}
              autoComplete="name"
              placeholder={m.poll_your_name_placeholder()}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="signup-identity-email">{m.poll_email_label()}</Label>
            <Input
              id="signup-identity-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              autoComplete="email"
              required={requireEmail}
              aria-describedby="signup-identity-email-hint"
            />
            <p id="signup-identity-email-hint" className="text-xs text-muted-foreground">
              {requireEmail ? m.poll_email_hint_required() : m.poll_email_hint_optional()}
            </p>
          </div>

          {needsCaptcha && <TurnstileField onToken={setCaptchaToken} />}

          {error && <FormError key={error.attempt}>{error.message}</FormError>}

          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              {m.common_cancel()}
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? m.signup_claiming() : m.signup_identity_submit()}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
