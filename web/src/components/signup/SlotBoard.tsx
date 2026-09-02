import { useMemo, useState } from 'react'
import { optionPlainLabel } from '#/components/poll/OptionHeader'
import type { ClaimBlockedReason } from '#/components/signup/ClaimButton'
import { IdentitySheet } from '#/components/signup/IdentitySheet'
import { SlotCard } from '#/components/signup/SlotCard'
import type { SlotClaimant } from '#/components/signup/ClaimantList'
import type { AppLocale } from '#/app.config'
import { m } from '#/lib/i18n'
import { useClaims } from '#/lib/use-claims'
import type { Session } from '#/lib/use-session'
import type { PollView } from '#/api/types'

/** Who is on each slot, in the order people signed up. */
function claimantsByOption(poll: PollView, yourParticipantId: string | null) {
  const map = new Map<string, SlotClaimant[]>()
  for (const option of poll.options) map.set(option.id, [])
  for (const participant of poll.participants) {
    for (const [optionId, answer] of Object.entries(participant.votes)) {
      if (answer !== 'yes') continue
      map.get(optionId)?.push({
        participantId: participant.id,
        name: participant.name,
        isYou: participant.id === yourParticipantId,
      })
    }
  }
  return map
}

/**
 * A sign-up sheet's slots, as a board of cards: one per slot, one column on a phone, two on a
 * tablet, three on a desktop.
 *
 * The board owns the one piece of flow the cards don't: a first-time claimer has no row yet, so
 * their first press opens the identity sheet and the claim happens when they submit it. After
 * that the browser holds an edit token and every further slot is a single click.
 */
export function SlotBoard({
  poll,
  session,
  locale,
  timeZone,
  onChanged,
}: {
  poll: PollView
  session: Session
  locale: AppLocale
  timeZone: string
  onChanged: () => void | Promise<void>
}) {
  const { viewer, claim, unclaim, pending, needsIdentity } = useClaims(poll, session, onChanged)
  const [identityFor, setIdentityFor] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const open = poll.status === 'open'
  const maxClaims = poll.settings.signupMaxClaims
  const claimants = useMemo(
    () => claimantsByOption(poll, viewer.participantId),
    [poll, viewer.participantId],
  )

  function blockedReason(full: boolean, claimedByYou: boolean) {
    if (!open) return 'closed' satisfies ClaimBlockedReason
    if (claimedByYou) return null
    if (full) return 'full' satisfies ClaimBlockedReason
    if (!viewer.canClaimMore) return 'limit' satisfies ClaimBlockedReason
    return null
  }

  function handleClaim(optionId: string) {
    if (needsIdentity) {
      setIdentityFor(optionId)
      return
    }
    void claim(optionId)
  }

  async function submitIdentity(values: {
    name: string
    email: string | undefined
    turnstileToken: string | undefined
  }) {
    if (identityFor === null) return
    setSubmitting(true)
    try {
      const ok = await claim(identityFor, values)
      if (ok) setIdentityFor(null)
    } finally {
      setSubmitting(false)
    }
  }

  const claimedCount = viewer.claimedOptionIds.length
  const identityOption =
    identityFor !== null ? poll.options.find((option) => option.id === identityFor) : undefined
  const identitySlotLabel = identityOption
    ? optionPlainLabel(identityOption, locale, timeZone)
    : undefined

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">
          {m.signup_board_title()}
        </h2>
        <p className="text-xs text-muted-foreground">
          {claimedCount > 0
            ? claimedCount === 1
              ? m.signup_your_claims_one()
              : m.signup_your_claims_other({ count: claimedCount })
            : maxClaims === 1
              ? m.signup_board_hint_one()
              : m.signup_board_hint_other({ count: maxClaims })}
        </p>
      </div>

      <div
        data-testid="slot-board"
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3"
      >
        {poll.options.map((option) => {
          const slot = poll.claims[option.id]
          const people = claimants.get(option.id) ?? []
          const count = slot?.count ?? people.length
          const capacity = slot?.capacity ?? option.capacity
          const full = slot?.full ?? (capacity !== null && count >= capacity)
          const claimedByYou = viewer.claimedOptionIds.includes(option.id)

          return (
            <SlotCard
              key={option.id}
              option={option}
              locale={locale}
              timeZone={timeZone}
              count={count}
              capacity={capacity}
              claimants={people}
              claimedByYou={claimedByYou}
              disabledReason={blockedReason(full, claimedByYou)}
              maxClaims={maxClaims}
              pending={pending.has(option.id)}
              isOwner={viewer.isOwner}
              onClaim={handleClaim}
              onUnclaim={(optionId, participantId) => void unclaim(optionId, participantId)}
            />
          )
        })}
      </div>

      <IdentitySheet
        key={identityFor ?? 'closed'}
        open={identityFor !== null}
        onOpenChange={(next) => {
          if (!next) setIdentityFor(null)
        }}
        requireEmail={poll.settings.requireParticipantEmail}
        needsCaptcha={session === null}
        defaultName={session?.user.name ?? ''}
        slotLabel={identitySlotLabel}
        submitting={submitting}
        onSubmit={submitIdentity}
      />
    </section>
  )
}
