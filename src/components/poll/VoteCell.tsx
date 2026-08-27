import { AnimatePresence, motion } from 'motion/react'
import { Check, Minus, X } from 'lucide-react'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { nextAnswer, type Answer } from '#/lib/scoring'
import { cn } from '#/lib/utils'

type Size = 'lg' | 'md' | 'sm'

/**
 * Answer colours. Each state pairs a soft fill with its ink so the mark clears AA against the
 * cell — a solid `--yes` fill with a white check reads bright but only manages ~2:1, and the
 * grid is the one place in the product where colour carries meaning.
 *
 * "Yes" additionally gets a full-strength ring so a column of yeses reads as solid from across
 * the room, which is what the grid is for.
 */
export const ANSWER_STYLES: Record<
  'yes' | 'ifneedbe' | 'no' | 'none',
  { className: string; Icon: typeof Check | null }
> = {
  yes: {
    className:
      'bg-yes-soft text-yes-ink ring-1 ring-[var(--yes)] hover:brightness-[0.97] dark:hover:brightness-110',
    Icon: Check,
  },
  ifneedbe: {
    className:
      'bg-ifneedbe-soft text-ifneedbe-ink ring-1 ring-[var(--ifneedbe)]/70 hover:brightness-[0.97] dark:hover:brightness-110',
    Icon: Minus,
  },
  no: {
    className: 'bg-no-soft text-no-ink hover:brightness-[0.97] dark:hover:brightness-110',
    Icon: X,
  },
  none: {
    className:
      'border border-dashed border-border bg-transparent text-muted-foreground hover:bg-secondary',
    Icon: null,
  },
}

const SIZES: Record<Size, string> = {
  // `lg` is the phone layout's target: a thumb needs the full 44px minimum, and the date list
  // has the width to spare that the grid never did.
  lg: 'size-13 shrink-0 rounded-xl',
  md: 'h-10 w-full min-w-10 rounded-xl',
  sm: 'h-8 w-full min-w-8 rounded-lg',
}

const ICON_SIZES: Record<Size, string> = { lg: 'size-5', md: 'size-4', sm: 'size-3.5' }

export function answerLabel(answer: Answer | null): string {
  if (answer === 'yes') return m.answer_yes()
  if (answer === 'ifneedbe') return m.answer_ifneedbe()
  if (answer === 'no') return m.answer_no()
  return m.answer_none()
}

function cellLabel(answer: Answer | null, optionLabel: string | undefined): string {
  const answerText = answerLabel(answer)
  if (!optionLabel) return answerText
  return m.poll_vote_cell_label({ option: optionLabel, answer: answerText })
}

export function Mark({ answer, size }: { answer: Answer | null; size: Size }) {
  const reduceMotion = useReducedMotion()
  const { Icon } = ANSWER_STYLES[answer ?? 'none']

  return (
    <AnimatePresence initial={false} mode="wait">
      <motion.span
        key={answer ?? 'none'}
        aria-hidden="true"
        initial={reduceMotion ? false : { opacity: 0, scale: 0.6 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={reduceMotion ? { opacity: 1 } : { opacity: 0, scale: 0.6 }}
        transition={{ duration: 0.12, ease: 'easeOut' }}
        className="flex items-center justify-center"
      >
        {Icon ? (
          <Icon className={ICON_SIZES[size]} strokeWidth={2.75} />
        ) : (
          <span
            className={cn(
              'rounded-full bg-current opacity-30',
              size === 'sm' ? 'size-1' : 'size-1.5',
            )}
          />
        )}
      </motion.span>
    </AnimatePresence>
  )
}

/**
 * One person's answer for one option. Tapping cycles yes → if need be → no → blank (skipping
 * "if need be" when the poll doesn't allow it). Read-only cells render as an image rather than a
 * button so screen-reader users aren't offered a control that does nothing.
 */
export function VoteCell({
  answer,
  onChange,
  allowIfNeedBe,
  readOnly = false,
  size = 'md',
  optionLabel,
  className,
}: {
  answer: Answer | null
  onChange?: (answer: Answer | null) => void
  allowIfNeedBe: boolean
  readOnly?: boolean
  size?: Size
  optionLabel?: string
  className?: string
}) {
  const reduceMotion = useReducedMotion()
  const state = answer ?? 'none'
  const shared = cn(
    'flex items-center justify-center transition-[background-color,box-shadow,filter] duration-200',
    SIZES[size],
    ANSWER_STYLES[state].className,
    className,
  )

  if (readOnly || !onChange) {
    return (
      <span
        role="img"
        aria-label={cellLabel(answer, optionLabel)}
        data-answer={state}
        className={shared}
      >
        <Mark answer={answer} size={size} />
      </span>
    )
  }

  return (
    <motion.button
      type="button"
      aria-label={cellLabel(answer, optionLabel)}
      aria-pressed={answer === 'yes'}
      data-answer={state}
      whileTap={reduceMotion ? undefined : { scale: 0.94 }}
      transition={spring}
      onClick={() => onChange(nextAnswer(answer, allowIfNeedBe))}
      className={cn(shared, 'focus-ring cursor-pointer')}
    >
      <Mark answer={answer} size={size} />
    </motion.button>
  )
}
