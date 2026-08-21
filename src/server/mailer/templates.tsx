import * as React from 'react'
import { render } from '@react-email/render'
import ClaimConfirmation from '../../../emails/ClaimConfirmation'
import Closed from '../../../emails/Closed'
import Digest from '../../../emails/Digest'
import Finalized from '../../../emails/Finalized'
import ResetPassword from '../../../emails/ResetPassword'
import VerifyEmail from '../../../emails/VerifyEmail'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'

type Rendered = { subject: string; html: string; text: string }

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

export async function renderDigest(p: {
  pollTitle: string
  pollUrl: string
  newVoters: string[]
  newComments: number
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
