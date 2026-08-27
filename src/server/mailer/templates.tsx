import * as React from 'react'
import { render } from '@react-email/render'
import ClaimConfirmation from '../../../emails/ClaimConfirmation'
import Closed from '../../../emails/Closed'
import Digest, { type DigestLine } from '../../../emails/Digest'
import Finalized from '../../../emails/Finalized'
import Notification, { notificationSubject } from '../../../emails/Notification'
import OrgInvite from '../../../emails/OrgInvite'
import ResetPassword from '../../../emails/ResetPassword'
import VerifyEmail from '../../../emails/VerifyEmail'
import { asLocaleOptions } from '#/lib/i18n'
import type { ImmediateEvent } from '#/lib/notifications'
import * as m from '#/paraglide/messages'

type Rendered = { subject: string; html: string; text: string }

export type { DigestLine }

async function renderEmail(subject: string, el: React.ReactElement): Promise<Rendered> {
  const [html, text] = await Promise.all([render(el), render(el, { plainText: true })])
  return { subject, html, text }
}

export async function renderVerifyEmail(p: {
  name: string
  url: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_verify_subject({}, t), <VerifyEmail {...p} />)
}

export async function renderResetPassword(p: {
  name: string
  url: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_reset_subject({}, t), <ResetPassword {...p} />)
}

export async function renderOrgInvite(p: {
  orgName: string
  inviterName: string
  url: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_org_invite_subject({ inviter: p.inviterName, org: p.orgName }, t),
    <OrgInvite {...p} />,
  )
}

export async function renderDigest(p: {
  pollTitle: string
  pollUrl: string
  lines: DigestLine[]
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_digest_subject({ title: p.pollTitle }, t), <Digest {...p} />)
}

export async function renderFinalized(p: {
  pollTitle: string
  pollUrl: string
  optionLabel: string
  recipientName: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_finalized_subject({ title: p.pollTitle }, t), <Finalized {...p} />)
}

export async function renderClosed(p: {
  pollTitle: string
  pollUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_closed_subject({ title: p.pollTitle }, t), <Closed {...p} />)
}

/**
 * One entry point for every immediate (non-digest) notification. `poll.closed` delegates to the
 * dedicated `Closed` template rather than duplicating copy that is already written and
 * translated; everything else renders through the generic `Notification` template.
 */
export async function renderNotification(p: {
  event: ImmediateEvent
  title: string
  url: string
  detail?: string
  locale: string
}): Promise<Rendered> {
  if (p.event === 'poll.closed') {
    return renderClosed({ pollTitle: p.title, pollUrl: p.url, locale: p.locale })
  }

  const t = asLocaleOptions(p.locale)
  return renderEmail(
    notificationSubject(p.event, p.title, t),
    <Notification
      event={p.event}
      title={p.title}
      url={p.url}
      detail={p.detail}
      locale={p.locale}
    />,
  )
}

export async function renderClaimConfirmation(p: {
  name: string
  pollTitle: string
  pollUrl: string
  slots: string[]
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(m.email_claim_subject({ title: p.pollTitle }, t), <ClaimConfirmation {...p} />)
}
