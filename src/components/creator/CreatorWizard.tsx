import { useEffect, useReducer, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import { AnimatePresence, motion } from 'motion/react'
import { ArrowLeft, ArrowRight, Sparkles } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { OptionsStep } from '#/components/creator/OptionsStep'
import { SettingsStep } from '#/components/creator/SettingsStep'
import { StepIndicator } from '#/components/creator/StepIndicator'
import { TypeStep } from '#/components/creator/TypeStep'
import {
  canAdvance,
  creatorReducer,
  draftToInput,
  initialDraft,
} from '#/components/creator/creator-state'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { createPollSchema } from '#/server/polls/schemas'
import { createPoll } from '#/server/polls/polls.functions'

const STEP_LABELS = [m.creator_step_basics, m.creator_step_options, m.creator_step_settings]

/**
 * The three-step poll creator.
 *
 * The draft starts in UTC and switches to the browser's zone on mount rather than reading `Intl`
 * during render: the server renders this page too, and workerd always resolves to UTC, so
 * resolving at render time would hand React a different timezone on the client and break
 * hydration.
 */
export function CreatorWizard() {
  const navigate = useNavigate()
  const reduceMotion = useReducedMotion()
  const createPollFn = useServerFn(createPoll)

  const [draft, dispatch] = useReducer(creatorReducer, 'UTC', initialDraft)
  const [direction, setDirection] = useState(1)
  const [submitting, setSubmitting] = useState(false)
  const moveFocus = useRef(false)

  useEffect(() => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (zone) dispatch({ type: 'setField', field: 'timezone', value: zone })
  }, [])

  function goTo(step: number) {
    if (step === draft.step) return
    setDirection(step > draft.step ? 1 : -1)
    moveFocus.current = true
    if (step > draft.step) dispatch({ type: 'next' })
    else dispatch({ type: 'setField', field: 'step', value: step })
  }

  async function handleSubmit() {
    const input = draftToInput(draft)
    // The server validates this too; parsing here turns "duplicate option" and friends into a
    // message on this page instead of a round trip that comes back as a generic failure.
    if (!createPollSchema.safeParse(input).success) {
      toast.error(m.creator_error_generic())
      return
    }

    setSubmitting(true)
    try {
      const { id } = await createPollFn({ data: input })
      toast.success(m.creator_created())
      // `/p/$id` lands in task 18, so this navigates by href rather than by route id.
      await navigate({ href: `/p/${id}?created=1` })
    } catch (error) {
      const code = errorCode(error)
      if (code === 'RATE_LIMITED') {
        toast.error(m.error_rate_limited())
      } else if (code === 'UNAUTHORIZED') {
        await navigate({ to: '/login', search: { next: '/new' } })
      } else {
        toast.error(m.creator_error_generic())
      }
    } finally {
      setSubmitting(false)
    }
  }

  const labels = STEP_LABELS.map((label) => label())
  const offset = reduceMotion ? 0 : direction * 28

  return (
    <div
      data-testid="creator-wizard"
      className="mx-auto flex w-full max-w-3xl flex-col px-5 py-10 sm:py-14"
    >
      <header className="flex flex-col gap-1">
        <h1 className="display text-3xl">{m.creator_page_title()}</h1>
        <p className="text-sm text-muted-foreground">{m.creator_page_subtitle()}</p>
      </header>

      <div className="mt-6 border-b border-border pb-px">
        <StepIndicator step={draft.step} labels={labels} onSelect={goTo} />
      </div>

      <p aria-live="polite" className="sr-only">
        {m.creator_step_progress({
          current: draft.step + 1,
          total: labels.length,
          label: labels[draft.step] ?? '',
        })}
      </p>

      <div className="mt-7">
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={draft.step}
            ref={(node) => {
              if (!node || !moveFocus.current) return
              moveFocus.current = false
              node.focus()
            }}
            tabIndex={-1}
            initial={{ opacity: 0, x: offset }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -offset }}
            transition={spring}
            className="outline-none"
          >
            {draft.step === 0 && (
              <TypeStep draft={draft} dispatch={dispatch} onNext={() => goTo(1)} />
            )}
            {draft.step === 1 && <OptionsStep draft={draft} dispatch={dispatch} />}
            {draft.step === 2 && <SettingsStep draft={draft} dispatch={dispatch} />}
          </motion.div>
        </AnimatePresence>
      </div>

      <div className="mt-9 flex items-center justify-between gap-3">
        {draft.step > 0 ? (
          <Button type="button" variant="ghost" onClick={() => goTo(draft.step - 1)}>
            <ArrowLeft aria-hidden="true" />
            {m.creator_back()}
          </Button>
        ) : (
          <span />
        )}

        {draft.step < 2 ? (
          <Button
            type="button"
            disabled={!canAdvance(draft)}
            onClick={() => goTo(draft.step + 1)}
            className="min-w-32"
          >
            {m.creator_next()}
            <ArrowRight aria-hidden="true" />
          </Button>
        ) : (
          <Button
            type="button"
            size="lg"
            disabled={submitting}
            onClick={() => void handleSubmit()}
            className="min-w-40"
          >
            <Sparkles aria-hidden="true" />
            {submitting ? m.creator_submitting() : m.creator_submit()}
          </Button>
        )}
      </div>
    </div>
  )
}
