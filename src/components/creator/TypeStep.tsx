import { useMemo, type Dispatch, type KeyboardEvent } from 'react'
import { CalendarDays, ClipboardList, ListChecks } from 'lucide-react'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Textarea } from '#/components/ui/textarea'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { LIMITS } from '#/server/polls/schemas'
import type { PollType } from '#/server/db/schema'
import type { CreatorAction, CreatorDraft } from '#/components/creator/creator-state'

/** The zones most samla organisers live in; the browser's own zone is added when it's missing. */
const COMMON_TIMEZONES = [
  'Europe/Oslo',
  'Europe/London',
  'Europe/Berlin',
  'UTC',
  'America/New_York',
  'America/Los_Angeles',
  'Asia/Tokyo',
  'Australia/Sydney',
] as const

function offsetLabel(timeZone: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone,
      timeZoneName: 'shortOffset',
    }).formatToParts(new Date())
    return parts.find((p) => p.type === 'timeZoneName')?.value ?? ''
  } catch {
    return ''
  }
}

function timezoneLabel(timeZone: string): string {
  const offset = offsetLabel(timeZone)
  const name = timeZone.replace(/_/g, ' ')
  return offset ? `${name} (${offset})` : name
}

const TYPE_CARDS = [
  {
    type: 'datetime' as const,
    icon: CalendarDays,
    title: () => m.creator_type_datetime_title(),
    description: () => m.creator_type_datetime_desc(),
  },
  {
    type: 'options' as const,
    icon: ListChecks,
    title: () => m.creator_type_options_title(),
    description: () => m.creator_type_options_desc(),
  },
  {
    type: 'signup' as const,
    icon: ClipboardList,
    title: () => m.creator_type_signup_title(),
    description: () => m.creator_type_signup_desc(),
  },
]

/**
 * Step 1: what kind of poll this is, and the words around it. The type cards are radio-like
 * buttons rather than a radio group so they can be big, tappable targets with their own copy.
 */
export function TypeStep({
  draft,
  dispatch,
  onNext,
  showTypeCards = true,
}: {
  draft: CreatorDraft
  dispatch: Dispatch<CreatorAction>
  onNext: () => void
  /** The edit page can't change a poll's type once it has options, so it hides these cards. */
  showTypeCards?: boolean
}) {
  const timezones = useMemo(() => {
    const zones: string[] = [...COMMON_TIMEZONES]
    if (draft.timezone && !zones.includes(draft.timezone)) zones.unshift(draft.timezone)
    return zones
  }, [draft.timezone])

  function setField(field: keyof CreatorDraft, value: unknown) {
    dispatch({ type: 'setField', field, value })
  }

  function setType(type: PollType) {
    setField('type', type)
  }

  function handleTitleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== 'Enter') return
    event.preventDefault()
    onNext()
  }

  return (
    <div className="flex flex-col gap-7">
      {showTypeCards && (
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-3 text-sm font-medium">{m.creator_type_legend()}</legend>
          <div className="grid gap-3 sm:grid-cols-3">
            {TYPE_CARDS.map((card) => {
              const selected = draft.type === card.type
              const Icon = card.icon

              return (
                <button
                  key={card.type}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => setType(card.type)}
                  className={cn(
                    'focus-ring group flex flex-col items-start gap-2 rounded-xl border p-4 text-left transition-all duration-200',
                    'hover:-translate-y-px hover:border-foreground/25',
                    selected
                      ? 'border-primary bg-accent-soft/60 shadow-[0_10px_26px_-18px_var(--primary)]'
                      : 'border-border bg-card',
                  )}
                >
                  <span
                    className={cn(
                      'flex size-9 items-center justify-center rounded-full transition-colors',
                      selected
                        ? 'bg-primary-strong text-primary-foreground'
                        : 'bg-muted text-muted-foreground group-hover:text-foreground',
                    )}
                  >
                    <Icon aria-hidden="true" className="size-4.5" />
                  </span>
                  <span className="font-medium">{card.title()}</span>
                  <span className="text-sm leading-snug text-muted-foreground text-pretty">
                    {card.description()}
                  </span>
                </button>
              )
            })}
          </div>
        </fieldset>
      )}

      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="creator-title">{m.creator_title_label()}</Label>
          <Input
            id="creator-title"
            autoFocus
            value={draft.title}
            maxLength={LIMITS.title}
            placeholder={m.creator_title_placeholder()}
            onChange={(e) => setField('title', e.target.value)}
            onKeyDown={handleTitleKeyDown}
            className="h-11 text-base md:text-base"
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="creator-description">
            {m.creator_description_label()}
            <span className="text-xs font-normal text-muted-foreground">
              {m.creator_optional()}
            </span>
          </Label>
          <Textarea
            id="creator-description"
            value={draft.description}
            maxLength={LIMITS.description}
            placeholder={m.creator_description_placeholder()}
            onChange={(e) => setField('description', e.target.value)}
            className="min-h-20"
          />
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="creator-location">
              {m.creator_location_label()}
              <span className="text-xs font-normal text-muted-foreground">
                {m.creator_optional()}
              </span>
            </Label>
            <Input
              id="creator-location"
              value={draft.location}
              maxLength={LIMITS.location}
              placeholder={m.creator_location_placeholder()}
              onChange={(e) => setField('location', e.target.value)}
              className="h-10"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="creator-timezone">{m.creator_timezone_label()}</Label>
            <select
              id="creator-timezone"
              value={draft.timezone}
              onChange={(e) => setField('timezone', e.target.value)}
              className="focus-ring h-10 w-full rounded-md border border-input bg-transparent px-3 text-sm shadow-xs transition-[color,box-shadow] focus-visible:border-ring dark:bg-input/30"
            >
              {timezones.map((zone) => (
                <option key={zone} value={zone}>
                  {timezoneLabel(zone)}
                </option>
              ))}
            </select>
            <p className="text-xs text-muted-foreground">{m.creator_timezone_hint()}</p>
          </div>
        </div>
      </div>
    </div>
  )
}
