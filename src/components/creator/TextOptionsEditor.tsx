import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { motion } from 'motion/react'
import { Plus, X } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { LIMITS } from '#/server/polls/schemas'

/**
 * A list of free-text options that behaves like a document rather than a form: Enter opens the
 * next line, Backspace on an empty line closes it again.
 *
 * The rows (including the blank ones a person is about to type into) are local state; `onChange`
 * only ever reports the trimmed, non-empty options, so the draft never carries placeholder rows
 * around and `canAdvance` can simply count what it is given.
 */
/**
 * Rows animate in but not out: an exiting row would linger in the accessibility tree with a
 * focusable input for the length of the animation, which is worse than an instant removal.
 */
const enter = { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, transition: spring }

export function TextOptionsEditor({
  value,
  onChange,
  max = LIMITS.options,
}: {
  value: string[]
  onChange: (options: string[]) => void
  max?: number
}) {
  const reduceMotion = useReducedMotion()
  const [rows, setRows] = useState<string[]>(() => (value.length > 0 ? value : ['', '']))
  const inputs = useRef<(HTMLInputElement | null)[]>([])
  const focusRow = useRef<number | null>(null)

  // Focus has to move after the new row list has rendered, so the target index is stashed by the
  // key handler and applied here.
  useEffect(() => {
    if (focusRow.current === null) return
    inputs.current[focusRow.current]?.focus()
    focusRow.current = null
  })

  function commit(next: string[]) {
    setRows(next)
    onChange(next.map((option) => option.trim()).filter((option) => option.length > 0))
  }

  function insertAfter(index: number) {
    if (rows.length >= max) return
    commit([...rows.slice(0, index + 1), '', ...rows.slice(index + 1)])
    focusRow.current = index + 1
  }

  function removeAt(index: number) {
    if (rows.length <= 1) return
    commit(rows.filter((_, i) => i !== index))
    focusRow.current = Math.max(index - 1, 0)
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>, index: number) {
    if (event.key === 'Enter') {
      event.preventDefault()
      insertAfter(index)
      return
    }
    if (event.key === 'Backspace' && rows[index] === '' && rows.length > 1) {
      event.preventDefault()
      removeAt(index)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-2">
        {rows.map((row, index) => (
          <motion.li
            key={`row-${index}`}
            layout={!reduceMotion}
            {...(reduceMotion ? {} : enter)}
            className="flex items-center gap-2"
          >
            <span
              aria-hidden="true"
              className="w-5 shrink-0 text-right text-xs tabular-nums text-muted-foreground"
            >
              {index + 1}
            </span>
            <Input
              ref={(el) => {
                inputs.current[index] = el
              }}
              value={row}
              aria-label={m.creator_text_label({ index: index + 1 })}
              placeholder={index === 0 ? m.creator_text_placeholder() : undefined}
              maxLength={LIMITS.optionLabel}
              onChange={(e) => commit(rows.map((r, i) => (i === index ? e.target.value : r)))}
              onKeyDown={(e) => handleKeyDown(e, index)}
              className="h-10"
            />
            {rows.length > 1 && (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={m.creator_text_remove({ index: index + 1 })}
                onClick={() => removeAt(index)}
                className="shrink-0 text-muted-foreground hover:text-foreground"
              >
                <X aria-hidden="true" />
              </Button>
            )}
          </motion.li>
        ))}
      </ul>

      <div className="flex items-center gap-3 pl-7">
        <Button
          type="button"
          variant="soft"
          size="sm"
          disabled={rows.length >= max}
          onClick={() => insertAfter(rows.length - 1)}
        >
          <Plus aria-hidden="true" />
          {m.creator_text_add()}
        </Button>
        <span className="text-xs text-muted-foreground">{m.creator_text_hint()}</span>
      </div>
    </div>
  )
}
