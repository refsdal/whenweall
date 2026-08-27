import { useMemo, useState } from 'react'
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
import { Textarea } from '#/components/ui/textarea'
import type { Interval } from '#/lib/availability'
import { getLocale, intlLocale, m } from '#/lib/i18n'

export type BookingFormValues = {
  startAt: string
  name: string
  email: string
  note: string | undefined
  turnstileToken: string | undefined
}

export const NOTE_MAX = 1000

/** "Tue 15 Sep · 09:00 – 09:30" in the visitor's own zone. */
export function slotSummary(slot: Interval, timeZone: string, locale: string): string {
  const day = new Intl.DateTimeFormat(intlLocale(locale), {
    timeZone,
    weekday: 'long',
    day: 'numeric',
    month: 'long',
  }).format(new Date(slot.start))
  const time = new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  return `${day} · ${time.format(new Date(slot.start))} – ${time.format(new Date(slot.end))}`
}

/**
 * The one form a visitor fills in: who they are, where to send the confirmation, and anything
 * the organiser should know. Everything above it is already decided — the chosen slot is
 * restated at the top so nobody confirms the wrong time.
 *
 * Validation is inline rather than a toast: the field that needs fixing is right there, and a
 * dialog steals focus from the toast region anyway (same reasoning as `IdentitySheet`).
 */
export function BookingForm({
  open,
  onOpenChange,
  title,
  location,
  slot,
  timeZone,
  submitting = false,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  location?: string | null
  slot: Interval
  timeZone: string
  submitting?: boolean
  onSubmit: (values: BookingFormValues) => void | Promise<void>
}) {
  const locale = getLocale()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [note, setNote] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [error, setError] = useState<FormFailure | null>(null)
  // A Turnstile token is good for exactly one submit, so the widget is remounted (via its key)
  // after every attempt — otherwise a retry after a taken slot would fail the captcha instead.
  const [attempt, setAttempt] = useState(0)

  const summary = useMemo(() => slotSummary(slot, timeZone, locale), [slot, timeZone, locale])
  const durationMin = Math.round(
    (new Date(slot.end).getTime() - new Date(slot.start).getTime()) / 60_000,
  )

  function submit() {
    const trimmedName = name.trim()
    if (!trimmedName) {
      setError((current) => nextFailure(current, m.poll_error_name_required()))
      return
    }
    const trimmedEmail = email.trim()
    if (!trimmedEmail) {
      setError((current) => nextFailure(current, m.book_form_error_email_required()))
      return
    }
    if (!z.email().safeParse(trimmedEmail).success) {
      setError((current) => nextFailure(current, m.poll_error_email_invalid()))
      return
    }
    if (!captchaToken) {
      setError((current) => nextFailure(current, m.poll_error_captcha()))
      return
    }

    setError(null)
    setCaptchaToken(null)
    setAttempt((value) => value + 1)
    void onSubmit({
      startAt: slot.start,
      name: trimmedName,
      email: trimmedEmail,
      note: note.trim() || undefined,
      turnstileToken: captchaToken,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{m.book_form_title()}</DialogTitle>
          <DialogDescription>{title}</DialogDescription>
        </DialogHeader>

        <div
          data-testid="booking-form-summary"
          className="rounded-xl bg-accent-soft px-4 py-3 text-sm text-accent-foreground"
        >
          <p className="font-medium first-letter:uppercase" suppressHydrationWarning>
            {summary}
          </p>
          <p className="text-xs opacity-80">
            {m.book_form_summary_duration({ count: durationMin })}
            {location ? ` · ${location}` : ''}
          </p>
        </div>

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
            <Label htmlFor="booking-name">{m.poll_your_name_label()}</Label>
            <Input
              id="booking-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={80}
              autoComplete="name"
              placeholder={m.poll_your_name_placeholder()}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="booking-email">{m.book_form_email_label()}</Label>
            <Input
              id="booking-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              autoComplete="email"
              required
              aria-describedby="booking-email-hint"
            />
            <p id="booking-email-hint" className="text-xs text-muted-foreground">
              {m.book_form_email_hint()}
            </p>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="booking-note">{m.book_form_note_label()}</Label>
            <Textarea
              id="booking-note"
              value={note}
              onChange={(event) => setNote(event.target.value.slice(0, NOTE_MAX))}
              rows={3}
              maxLength={NOTE_MAX}
              placeholder={m.book_form_note_placeholder()}
            />
          </div>

          <TurnstileField key={attempt} onToken={setCaptchaToken} />

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
              {submitting ? m.book_form_submitting() : m.book_form_submit()}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
