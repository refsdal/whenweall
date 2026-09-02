import { useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { MessageSquare, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { TurnstileField } from '#/components/auth/TurnstileField'
import type { ViewerState } from '#/components/poll/viewer'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Textarea } from '#/components/ui/textarea'
import { errorCode } from '#/lib/errors'
import { intlLocale, m } from '#/lib/i18n'
import { listItem, useReducedMotion } from '#/lib/motion'
import type { Session } from '#/lib/use-session'
import { addComment, deleteComment, LIMITS } from '#/api/polls'
import type { CommentView, PollView } from '#/api/types'

function initial(name: string): string {
  return name.trim().slice(0, 1).toUpperCase() || '?'
}

/**
 * The conversation under the grid: "I can do Tuesday if we start late". Guests can join in with
 * a name and a captcha; the organiser and the author can delete.
 */
export function Comments({
  poll,
  session,
  viewer,
  canComment,
  viewerName,
  onChanged,
}: {
  poll: PollView
  session: Session
  viewer: ViewerState
  canComment: boolean
  viewerName: string
  onChanged: () => void | Promise<void>
}) {
  const reduceMotion = useReducedMotion()

  const [name, setName] = useState(viewerName)
  const [body, setBody] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const isGuest = session === null
  // `Intl.DateTimeFormat` construction is the expensive part; the comment list re-renders on
  // every keystroke in the composer, so build it once per locale/time-zone pair.
  const formatter = useMemo(
    () =>
      new Intl.DateTimeFormat(intlLocale(viewer.locale), {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: viewer.timeZone,
      }),
    [viewer.locale, viewer.timeZone],
  )

  function canDelete(comment: CommentView): boolean {
    if (poll.isOwner) return true
    return session !== null && comment.userId === session.user.id
  }

  async function submit() {
    const trimmedName = name.trim()
    const trimmedBody = body.trim()
    if (!trimmedName) {
      toast.error(m.poll_error_name_required())
      return
    }
    if (!trimmedBody) return
    if (isGuest && !captchaToken) {
      toast.error(m.poll_error_captcha())
      return
    }

    setSubmitting(true)
    try {
      await addComment(
        poll.id,
        { authorName: trimmedName, body: trimmedBody },
        { captchaToken: captchaToken ?? undefined, guestToken: viewer.editToken ?? undefined },
      )
      setBody('')
      toast.success(m.poll_comment_posted())
      await onChanged()
    } catch (error) {
      toast.error(
        errorCode(error) === 'rate_limited' ? m.error_rate_limited() : m.poll_comment_error(),
      )
    } finally {
      setSubmitting(false)
    }
  }

  async function remove(commentId: string) {
    try {
      await deleteComment(poll.id, commentId)
      toast.success(m.poll_comment_deleted())
      await onChanged()
    } catch {
      toast.error(m.poll_error_generic())
    }
  }

  return (
    <section aria-labelledby="poll-comments-heading" className="flex flex-col gap-4">
      <h2 id="poll-comments-heading" className="display flex items-center gap-2 text-lg">
        <MessageSquare aria-hidden="true" className="size-4 text-muted-foreground" />
        {m.poll_comments_title()}
        {poll.comments.length > 0 && (
          <span className="text-sm font-normal text-muted-foreground tabular-nums">
            {poll.comments.length}
          </span>
        )}
      </h2>

      {poll.comments.length === 0 ? (
        <p className="text-sm text-muted-foreground">{m.poll_comments_empty()}</p>
      ) : (
        <ul className="flex flex-col gap-3">
          <AnimatePresence initial={false}>
            {poll.comments.map((comment) => (
              <motion.li
                key={comment.id}
                layout={reduceMotion ? false : 'position'}
                initial={reduceMotion ? false : listItem.initial}
                animate={listItem.animate}
                exit={reduceMotion ? { opacity: 0 } : listItem.exit}
                transition={listItem.transition}
                className="surface group/comment flex gap-3 p-3.5"
              >
                <span
                  aria-hidden="true"
                  className="inline-flex size-7 shrink-0 items-center justify-center rounded-full bg-secondary text-[0.6875rem] font-semibold text-secondary-foreground"
                >
                  {initial(comment.authorName)}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-baseline gap-x-2">
                    <span className="text-sm font-medium">{comment.authorName}</span>
                    <time
                      dateTime={comment.createdAt}
                      suppressHydrationWarning
                      className="text-xs text-muted-foreground"
                    >
                      {formatter.format(new Date(comment.createdAt))}
                    </time>
                  </p>
                  <p className="mt-1 text-sm wrap-anywhere whitespace-pre-wrap">{comment.body}</p>
                </div>
                {canDelete(comment) && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={m.poll_comment_delete({ name: comment.authorName })}
                    onClick={() => void remove(comment.id)}
                    className="shrink-0 opacity-70 transition-opacity focus-visible:opacity-100 group-hover/comment:opacity-100 sm:opacity-0"
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                )}
              </motion.li>
            ))}
          </AnimatePresence>
        </ul>
      )}

      {canComment && (
        <div className="flex flex-col gap-2">
          {!session && (
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={LIMITS.name}
              aria-label={m.poll_your_name_label()}
              placeholder={m.poll_your_name_placeholder()}
              className="sm:max-w-xs"
            />
          )}
          <Textarea
            value={body}
            onChange={(event) => setBody(event.target.value)}
            maxLength={LIMITS.comment}
            rows={3}
            aria-label={m.poll_comments_title()}
            placeholder={m.poll_comment_placeholder()}
          />
          {isGuest && <TurnstileField onToken={setCaptchaToken} />}
          <div className="flex justify-end">
            <Button
              type="button"
              variant="outline"
              disabled={submitting || body.trim().length === 0}
              onClick={() => void submit()}
            >
              {submitting ? m.poll_comment_posting() : m.poll_comment_submit()}
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}
