import { useMemo, useState, type Dispatch } from 'react'
import { CalendarClock, CircleHelp, MapPin, MessageSquare, Mail, Users } from 'lucide-react'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Switch } from '#/components/ui/switch'
import { optionCountLabel } from '#/components/creator/OptionsStep'
import {
  countOptions,
  signupCapacitySummary,
  type CreatorAction,
  type CreatorDraft,
} from '#/components/creator/creator-state'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { localToUtcIso, utcIsoToLocalParts } from '#/lib/time'

/** A sensible first guess: the day before the earliest option, otherwise tomorrow. */
function defaultDeadlineDate(draft: CreatorDraft): string {
  const earliest = draft.dates[0]?.date
  const base = earliest ? new Date(`${earliest}T00:00:00Z`) : new Date()
  base.setUTCDate(base.getUTCDate() - (earliest ? 1 : -1))
  return base.toISOString().slice(0, 10)
}

function SettingRow({
  id,
  icon: Icon,
  label,
  description,
  checked,
  onCheckedChange,
}: {
  id: string
  icon: typeof MessageSquare
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-start gap-3 py-3">
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <Label htmlFor={id} className="cursor-pointer">
          {label}
        </Label>
        <p className="text-sm text-pretty text-muted-foreground">{description}</p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} className="mt-1" />
    </div>
  )
}

/** Step 3: the few knobs worth having, plus a plain-language summary of what's about to exist. */
export function SettingsStep({
  draft,
  dispatch,
}: {
  draft: CreatorDraft
  dispatch: Dispatch<CreatorAction>
}) {
  const locale = getLocale()
  const stored = draft.deadlineAt ? utcIsoToLocalParts(draft.deadlineAt, draft.timezone) : null
  const isSignup = draft.type === 'signup'

  const [enabled, setEnabled] = useState(draft.deadlineAt !== null)
  const [date, setDate] = useState(stored?.date ?? defaultDeadlineDate(draft))
  const [time, setTime] = useState(stored?.time ?? '12:00')
  const [maxClaimsText, setMaxClaimsText] = useState(String(draft.signupMaxClaims))
  const [syncedMaxClaims, setSyncedMaxClaims] = useState(draft.signupMaxClaims)
  // Adjusted during render (not an effect) so the field's own draft text stays in sync the moment
  // `draft.signupMaxClaims` changes from outside — same pattern as `CapacityField`.
  if (draft.signupMaxClaims !== syncedMaxClaims) {
    setSyncedMaxClaims(draft.signupMaxClaims)
    setMaxClaimsText(String(draft.signupMaxClaims))
  }

  const count = countOptions(draft)
  const { slots, spots } = signupCapacitySummary(draft)

  const deadlineText = useMemo(() => {
    if (!draft.deadlineAt) return m.creator_deadline_none()
    return new Intl.DateTimeFormat(intlLocale(locale), {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: draft.timezone,
    }).format(new Date(draft.deadlineAt))
  }, [draft.deadlineAt, draft.timezone, locale])

  function setField(field: keyof CreatorDraft, value: unknown) {
    dispatch({ type: 'setField', field, value })
  }

  function syncDeadline(next: { enabled?: boolean; date?: string; time?: string }) {
    const on = next.enabled ?? enabled
    const nextDate = next.date ?? date
    const nextTime = next.time ?? time
    setField(
      'deadlineAt',
      on && nextDate && nextTime ? localToUtcIso(nextDate, nextTime, draft.timezone) : null,
    )
  }

  return (
    <div className="flex flex-col gap-7">
      <section className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
        <div className="flex items-start gap-3">
          <CalendarClock
            aria-hidden="true"
            className="mt-0.5 size-4 shrink-0 text-muted-foreground"
          />
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <Label htmlFor="creator-deadline" className="cursor-pointer">
              {m.creator_deadline_title()}
            </Label>
            <p className="text-sm text-muted-foreground">
              {enabled
                ? m.creator_deadline_hint({ timezone: draft.timezone.replace(/_/g, ' ') })
                : m.creator_deadline_none()}
            </p>
          </div>
          <Switch
            id="creator-deadline"
            checked={enabled}
            onCheckedChange={(next) => {
              setEnabled(next)
              syncDeadline({ enabled: next })
            }}
            className="mt-1"
          />
        </div>

        {enabled && (
          <div className="flex flex-wrap gap-3 pl-7">
            <div className="flex flex-col gap-1">
              <Label htmlFor="creator-deadline-date" className="text-xs text-muted-foreground">
                {m.creator_deadline_date_label()}
              </Label>
              <Input
                id="creator-deadline-date"
                type="date"
                value={date}
                onChange={(e) => {
                  setDate(e.target.value)
                  syncDeadline({ date: e.target.value })
                }}
                className="h-9 w-[10.5rem]"
              />
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="creator-deadline-time" className="text-xs text-muted-foreground">
                {m.creator_deadline_time_label()}
              </Label>
              <Input
                id="creator-deadline-time"
                type="time"
                value={time}
                onChange={(e) => {
                  setTime(e.target.value)
                  syncDeadline({ time: e.target.value })
                }}
                className="h-9 w-[7.5rem] tabular-nums"
              />
            </div>
          </div>
        )}
      </section>

      {isSignup && (
        <section className="flex flex-col gap-1.5 rounded-xl border border-border bg-card p-4">
          <div className="flex items-start gap-3">
            <Users aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <Label htmlFor="creator-max-claims">{m.creator_signup_maxclaims_label()}</Label>
              <p className="text-sm text-muted-foreground">{m.creator_signup_maxclaims_desc()}</p>
            </div>
          </div>
          <Input
            id="creator-max-claims"
            type="number"
            inputMode="numeric"
            min={1}
            max={100}
            step={1}
            value={maxClaimsText}
            className="ml-7 h-9 w-24 tabular-nums"
            onChange={(e) => {
              setMaxClaimsText(e.target.value)
              const parsed = Number(e.target.value)
              if (Number.isInteger(parsed) && parsed >= 1 && parsed <= 100) {
                setField('signupMaxClaims', parsed)
              }
            }}
            onBlur={() => setMaxClaimsText(String(draft.signupMaxClaims))}
          />
        </section>
      )}

      <section className="flex flex-col divide-y divide-border rounded-xl border border-border bg-card px-4">
        {!isSignup && (
          <SettingRow
            id="creator-ifneedbe"
            icon={CircleHelp}
            label={m.creator_allow_ifneedbe_label()}
            description={m.creator_allow_ifneedbe_desc()}
            checked={draft.allowIfNeedBe}
            onCheckedChange={(next) => setField('allowIfNeedBe', next)}
          />
        )}
        <SettingRow
          id="creator-comments"
          icon={MessageSquare}
          label={m.creator_allow_comments_label()}
          description={m.creator_allow_comments_desc()}
          checked={draft.allowComments}
          onCheckedChange={(next) => setField('allowComments', next)}
        />
        <SettingRow
          id="creator-email"
          icon={Mail}
          label={m.creator_require_email_label()}
          description={m.creator_require_email_desc()}
          checked={draft.requireParticipantEmail}
          onCheckedChange={(next) => setField('requireParticipantEmail', next)}
        />
      </section>

      <section className="surface flex flex-col gap-1 p-4">
        <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.creator_summary_title()}
        </h3>
        <p className="display text-lg break-words">{draft.title.trim()}</p>
        <p className="text-sm text-muted-foreground">
          {draft.type === 'datetime'
            ? m.creator_summary_type_datetime()
            : draft.type === 'options'
              ? m.creator_summary_type_options()
              : m.creator_summary_type_signup()}
          {' · '}
          {isSignup
            ? spots === null
              ? m.creator_summary_spots_unlimited({ slots })
              : m.creator_summary_spots_total({ slots, spots })
            : optionCountLabel(count)}
          {' · '}
          {deadlineText}
        </p>
        {draft.location.trim() && (
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <MapPin aria-hidden="true" className="size-3.5" />
            {draft.location.trim()}
          </p>
        )}
      </section>
    </div>
  )
}
