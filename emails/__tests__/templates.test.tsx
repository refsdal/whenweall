import { describe, expect, it } from 'vitest'
import {
  renderClaimConfirmation,
  renderClosed,
  renderDigest,
  renderFinalized,
  renderNotification,
  renderResetPassword,
  renderVerifyEmail,
} from '#/server/mailer/templates'
import {
  renderBookingCancelled,
  renderBookingConfirmed,
  renderBookingOrganiserNotice,
  renderBookingReminder,
  renderBookingRescheduled,
  renderBookingRescheduledOrganiser,
  renderBookingSyncFailed,
} from '#/server/bookings/emails'

describe('renderVerifyEmail', () => {
  it('renders english', async () => {
    const { subject, html, text } = await renderVerifyEmail({
      name: 'Ada',
      url: 'https://x/verify?token=1',
      locale: 'en',
    })

    expect(subject).toContain('Verify')
    expect(html).toContain('https://x/verify?token=1')
    expect(html).toContain('Ada')
    expect(text).toContain('https://x/verify?token=1')
  })

  it('renders norwegian', async () => {
    const { subject } = await renderVerifyEmail({
      name: 'Ada',
      url: 'https://x/verify?token=1',
      locale: 'nb',
    })

    expect(subject).toContain('Bekreft')
  })
})

describe('renderResetPassword', () => {
  it('renders with url and name', async () => {
    const { subject, html, text } = await renderResetPassword({
      name: 'Ada',
      url: 'https://x/reset?token=1',
      locale: 'en',
    })

    expect(subject.length).toBeGreaterThan(0)
    expect(html).toContain('https://x/reset?token=1')
    expect(html).toContain('Ada')
    expect(text).toContain('https://x/reset?token=1')
  })
})

describe('renderDigest', () => {
  it('lists voter names and includes the poll title in the subject', async () => {
    const { subject, html } = await renderDigest({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      lines: [{ event: 'response.created', names: ['Ada', 'Grace', 'Rosalind'], count: 3 }],
      locale: 'en',
    })

    expect(subject).toContain('Team sync')
    expect(html).toContain('Ada')
    expect(html).toContain('Grace')
    expect(html).toContain('Rosalind')
    expect(html).toContain('https://x/p/abc')
  })

  it('renders one line per event kind so edits read differently from new responses', async () => {
    const { html } = await renderDigest({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      lines: [
        { event: 'response.created', names: ['Ada'], count: 1 },
        { event: 'response.updated', names: ['Cleo'], count: 1 },
        { event: 'comment.created', names: [], count: 3 },
      ],
      locale: 'en',
    })

    expect(html).toMatch(/1 new responses/)
    expect(html).toMatch(/1 changed responses/)
    expect(html).toMatch(/3 new comments/)
    expect(html).toContain('Cleo')
  })

  it('translates the lines', async () => {
    const { html } = await renderDigest({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      lines: [{ event: 'response.updated', names: ['Cleo'], count: 2 }],
      locale: 'nb',
    })

    expect(html).toContain('endrede svar')
  })
})

describe('renderNotification', () => {
  it('renders a lifecycle event with its own subject', async () => {
    const { subject, html } = await renderNotification({
      event: 'deadline.approaching',
      title: 'Team sync',
      url: 'https://x/p/abc',
      locale: 'en',
    })

    expect(subject).toContain('Team sync')
    expect(html).toMatch(/deadline/i)
    expect(html).toContain('https://x/p/abc')
  })

  it('delegates poll.closed to the dedicated Closed template', async () => {
    const { subject } = await renderNotification({
      event: 'poll.closed',
      title: 'Team sync',
      url: 'https://x/p/abc',
      locale: 'en',
    })

    expect(subject).toContain('closed')
  })

  it('includes the booking detail', async () => {
    const { html } = await renderNotification({
      event: 'booking.created',
      title: 'Intro call',
      url: 'https://x/bookings/abc',
      detail: '2026-09-01T10:00:00.000Z',
      locale: 'en',
    })

    expect(html).toContain('2026-09-01T10:00:00.000Z')
  })
})

describe('renderFinalized', () => {
  it('includes the winning option label', async () => {
    const { html, subject } = await renderFinalized({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      optionLabel: 'Wed 10:00 – 11:00',
      recipientName: 'Ada',
      locale: 'en',
    })

    expect(html).toContain('Wed 10:00 – 11:00')
    expect(subject).toContain('Team sync')
  })
})

describe('renderClosed', () => {
  it('includes the poll title and url', async () => {
    const { html, subject } = await renderClosed({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      locale: 'en',
    })

    expect(subject).toContain('Team sync')
    expect(html).toContain('https://x/p/abc')
  })
})

describe('renderClaimConfirmation', () => {
  it('lists claimed slots and includes the poll title/url (english)', async () => {
    const { subject, html, text } = await renderClaimConfirmation({
      name: 'Ada',
      pollTitle: 'Bake sale',
      pollUrl: 'https://x/p/abc',
      slots: ['Sat 12 Sep', 'Bring cookies'],
      locale: 'en',
    })

    expect(subject).toContain('Bake sale')
    expect(html).toContain('Ada')
    expect(html).toContain('Sat 12 Sep')
    expect(html).toContain('Bring cookies')
    expect(html).toContain('https://x/p/abc')
    expect(text).toContain('Sat 12 Sep')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderClaimConfirmation({
      name: 'Ada',
      pollTitle: 'Bake sale',
      pollUrl: 'https://x/p/abc',
      slots: ['Lørdag 12. sep'],
      locale: 'nb',
    })

    expect(subject).toContain('Bake sale')
    expect(html).toContain('påmeldt')
    expect(html).toContain('Lørdag 12. sep')
  })
})

describe('renderBookingConfirmed', () => {
  it('includes the visitor, organiser, time and manage link (english)', async () => {
    const { subject, html, text } = await renderBookingConfirmed({
      visitorName: 'Bob',
      pageTitle: '15 min intro',
      organiserName: 'Ada',
      when: 'Wed 10 Sep, 09:00–09:15',
      location: 'Zoom',
      manageUrl: 'https://x/booking/abc?t=tok',
      locale: 'en',
    })

    expect(subject).toContain('15 min intro')
    expect(html).toContain('Bob')
    expect(html).toContain('Ada')
    expect(html).toContain('Wed 10 Sep, 09:00–09:15')
    expect(html).toContain('Zoom')
    expect(html).toContain('https://x/booking/abc?t=tok')
    expect(text).toContain('https://x/booking/abc?t=tok')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderBookingConfirmed({
      visitorName: 'Bob',
      pageTitle: '15 min intro',
      organiserName: 'Ada',
      when: 'ons 10. sep, 09:00–09:15',
      manageUrl: 'https://x/booking/abc?t=tok',
      locale: 'nb',
    })

    expect(subject).toContain('Bekreftet')
    expect(html).toContain('bekreftet')
  })
})

describe('renderBookingOrganiserNotice', () => {
  it('includes visitor name/email/note and the booking time', async () => {
    const { subject, html } = await renderBookingOrganiserNotice({
      organiserName: 'Ada',
      pageTitle: '15 min intro',
      visitorName: 'Bob',
      visitorEmail: 'bob@example.com',
      visitorNote: 'Looking forward to it',
      when: 'Wed 10 Sep, 09:00–09:15',
      viewUrl: 'https://x/bookings/p1',
      locale: 'en',
    })

    expect(subject).toContain('New booking')
    expect(html).toContain('Bob')
    expect(html).toContain('bob@example.com')
    expect(html).toContain('Looking forward to it')
    expect(html).toContain('Wed 10 Sep, 09:00–09:15')
  })

  it('omits the note line when there is none', async () => {
    const { html } = await renderBookingOrganiserNotice({
      organiserName: 'Ada',
      pageTitle: '15 min intro',
      visitorName: 'Bob',
      visitorEmail: 'bob@example.com',
      when: 'Wed 10 Sep, 09:00–09:15',
      viewUrl: 'https://x/bookings/p1',
      locale: 'en',
    })

    expect(html).not.toContain('Note:')
  })
})

describe('renderBookingCancelled', () => {
  it('phrases "you cancelled" for the party who cancelled', async () => {
    const { html } = await renderBookingCancelled({
      recipientName: 'Bob',
      pageTitle: '15 min intro',
      when: 'Wed 10 Sep, 09:00–09:15',
      cancelledBy: 'you',
      viewUrl: 'https://x/book/ada/intro',
      locale: 'en',
    })

    expect(html).toContain('you cancelled')
  })

  it("phrases the other party's cancellation from the organiser's side", async () => {
    const { html } = await renderBookingCancelled({
      recipientName: 'Ada',
      pageTitle: '15 min intro',
      when: 'Wed 10 Sep, 09:00–09:15',
      cancelledBy: 'visitor',
      visitorName: 'Bob',
      viewUrl: 'https://x/bookings/p1',
      locale: 'en',
    })

    expect(html).toContain('Bob cancelled')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderBookingCancelled({
      recipientName: 'Bob',
      pageTitle: '15 min intro',
      when: 'ons 10. sep, 09:00–09:15',
      cancelledBy: 'organiser',
      viewUrl: 'https://x/book/ada/intro',
      locale: 'nb',
    })

    expect(subject).toContain('Avlyst')
    expect(html).toContain('avlyste')
  })
})

describe('renderBookingReminder', () => {
  it('includes the recipient name, time and location', async () => {
    const { subject, html } = await renderBookingReminder({
      recipientName: 'Bob',
      pageTitle: '15 min intro',
      when: 'Wed 10 Sep, 09:00–09:15',
      location: 'Zoom',
      viewUrl: 'https://x/booking/abc?t=tok',
      locale: 'en',
    })

    expect(subject).toContain('Reminder')
    expect(html).toContain('Bob')
    expect(html).toContain('Wed 10 Sep, 09:00–09:15')
    expect(html).toContain('Zoom')
  })
})

describe('renderBookingRescheduled', () => {
  it('mentions both the previous and new time', async () => {
    const { subject, html, text } = await renderBookingRescheduled({
      visitorName: 'Bob',
      pageTitle: '15 min intro',
      organiserName: 'Ada',
      previousWhen: 'Wed 10 Sep, 09:00',
      when: 'Thu 11 Sep, 14:00–14:15',
      manageUrl: 'https://x/booking/abc?t=tok',
      locale: 'en',
    })

    expect(subject).toContain('Rescheduled')
    expect(html).toContain('Bob')
    expect(html).toContain('Wed 10 Sep, 09:00')
    expect(html).toContain('Thu 11 Sep, 14:00–14:15')
    expect(text).toContain('https://x/booking/abc?t=tok')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderBookingRescheduled({
      visitorName: 'Bob',
      pageTitle: '15 min intro',
      organiserName: 'Ada',
      previousWhen: 'ons 10. sep, 09:00',
      when: 'tor 11. sep, 14:00–14:15',
      manageUrl: 'https://x/booking/abc?t=tok',
      locale: 'nb',
    })

    expect(subject).toContain('Flyttet')
    expect(html).toContain('flyttet')
  })
})

describe('renderBookingRescheduledOrganiser', () => {
  it('mentions the previous and new time, and never says "New booking"', async () => {
    const { subject, html } = await renderBookingRescheduledOrganiser({
      organiserName: 'Ada',
      pageTitle: '15 min intro',
      visitorName: 'Bob',
      previousWhen: 'Wed 10 Sep, 09:00',
      when: 'Thu 11 Sep, 14:00–14:15',
      viewUrl: 'https://x/bookings/p1',
      locale: 'en',
    })

    expect(subject).toContain('moved')
    expect(subject).not.toContain('New booking')
    expect(html).toContain('Bob')
    expect(html).toContain('Wed 10 Sep, 09:00')
    expect(html).toContain('Thu 11 Sep, 14:00–14:15')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderBookingRescheduledOrganiser({
      organiserName: 'Ada',
      pageTitle: '15 min intro',
      visitorName: 'Bob',
      previousWhen: 'ons 10. sep, 09:00',
      when: 'tor 11. sep, 14:00–14:15',
      viewUrl: 'https://x/bookings/p1',
      locale: 'nb',
    })

    expect(subject).toContain('flyttet')
    expect(html).toContain('flyttet')
  })
})

describe('renderBookingSyncFailed', () => {
  it('mentions the page title and gives no false booking-specific detail', async () => {
    const { subject, html } = await renderBookingSyncFailed({
      pageTitle: '15 min intro',
      locale: 'en',
    })

    expect(subject).toContain('15 min intro')
    expect(html).toContain('15 min intro')
    expect(html).toContain('update your Google Calendar')
  })

  it('renders norwegian', async () => {
    const { subject, html } = await renderBookingSyncFailed({
      pageTitle: '15 min intro',
      locale: 'nb',
    })

    expect(subject).toContain('synkronisering')
    expect(html).toContain('kalenderen din')
  })
})
