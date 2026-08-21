import { useMemo, type Dispatch } from 'react'
import { motion } from 'motion/react'
import { enGB, nb } from 'date-fns/locale'
import { CalendarDays, X } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { Calendar } from '#/components/ui/calendar'
import { TimeSlotEditor } from '#/components/creator/TimeSlotEditor'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import type { CreatorAction, CreatorDraft } from '#/components/creator/creator-state'

/** Local calendar day → the `YYYY-MM-DD` key the draft stores (never a UTC shift). */
function toKey(date: Date): string {
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

function fromKey(key: string): Date {
  const [y, mo, d] = key.split('-').map(Number) as [number, number, number]
  return new Date(y, mo - 1, d)
}

/**
 * Step 2 for date polls: a calendar on the left, the chosen days on the right. Each day is an
 * all-day option until times are added to it, at which point every time window becomes its own
 * option.
 */
export function DateOptionsEditor({
  draft,
  dispatch,
}: {
  draft: CreatorDraft
  dispatch: Dispatch<CreatorAction>
}) {
  const reduceMotion = useReducedMotion()
  const locale = getLocale()

  const selected = useMemo(() => draft.dates.map((day) => fromKey(day.date)), [draft.dates])
  const today = useMemo(() => {
    const now = new Date()
    return new Date(now.getFullYear(), now.getMonth(), now.getDate())
  }, [])
  const dayFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(intlLocale(locale), {
        weekday: 'long',
        day: 'numeric',
        month: 'short',
      }),
    [locale],
  )

  function toggleDate(date: string) {
    dispatch({ type: 'toggleDate', date })
  }

  return (
    <div className="grid gap-5 lg:grid-cols-[auto_minmax(0,1fr)] lg:gap-7">
      <div className="surface w-fit max-lg:mx-auto">
        <Calendar
          mode="multiple"
          selected={selected}
          locale={locale === 'nb' ? nb : enGB}
          disabled={{ before: today }}
          startMonth={today}
          showOutsideDays={false}
          onSelect={(_selected, triggerDate) => toggleDate(toKey(triggerDate))}
          className="w-fit bg-transparent p-3 [--cell-size:--spacing(9)]"
        />
      </div>

      {draft.dates.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 py-10 text-center">
          <CalendarDays aria-hidden="true" className="size-6 text-muted-foreground/70" />
          <p className="font-medium">{m.creator_dates_empty_title()}</p>
          <p className="max-w-xs text-sm text-balance text-muted-foreground">
            {m.creator_dates_empty_body()}
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <h3 className="text-sm font-medium text-muted-foreground">{m.creator_selected_days()}</h3>
          <ul className="flex flex-col gap-2.5 lg:max-h-[26rem] lg:overflow-y-auto lg:pr-1">
            {draft.dates.map((day) => {
              const label = dayFormatter.format(fromKey(day.date))

              return (
                <motion.li
                  key={day.date}
                  layout={!reduceMotion}
                  initial={reduceMotion ? false : { opacity: 0, y: 6 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={spring}
                  className="flex flex-col gap-2.5 rounded-xl border border-border bg-card p-3"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex flex-col">
                      <span className="font-medium first-letter:uppercase">{label}</span>
                      {day.slots.length === 0 && (
                        <span className="text-xs text-muted-foreground">{m.creator_allday()}</span>
                      )}
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={m.creator_day_remove({ day: label })}
                      onClick={() => toggleDate(day.date)}
                      className="-mt-1 -mr-1 shrink-0 text-muted-foreground hover:text-foreground"
                    >
                      <X aria-hidden="true" />
                    </Button>
                  </div>

                  <TimeSlotEditor
                    date={day.date}
                    slots={day.slots}
                    onAdd={(start, end) =>
                      dispatch({ type: 'addSlot', date: day.date, start, end })
                    }
                    onRemove={(index) => dispatch({ type: 'removeSlot', date: day.date, index })}
                    onApplyToAll={
                      draft.dates.length > 1
                        ? () => {
                            dispatch({ type: 'applySlotsToAll', fromDate: day.date })
                            toast.success(m.creator_apply_to_all_done())
                          }
                        : undefined
                    }
                  />
                </motion.li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
