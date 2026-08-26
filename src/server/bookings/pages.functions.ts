import { createServerFn } from '@tanstack/react-start'
import * as z from 'zod'
import { getDb } from '#/server/db/client'
import { requireSessionMiddleware } from '#/server/auth/middleware'
import { requireOrgMiddleware, requireOwnerRole } from '#/server/auth/org'
import { getGoogleCalendarStatus as googleCalendarStatus } from '#/server/google/calendar'
import { notifyPageChanged } from '#/server/notifications/booking-client'
import * as pagesService from './pages'
import { createBookingPageSchema, handleSchema, updateBookingPageSchema } from './schemas'

/*
 * A `createServerFn(...)` object doesn't expose its `.middleware([...])` array at runtime (only
 * `method` and `__executeServer` — see test/server-functions.workers.test.ts for how that was
 * confirmed), so this array is declared once here and reused both to build each function below
 * and as the manifest that test asserts against. Reusing the same array reference means the
 * manifest can never drift from what a function actually runs.
 */
const REQUIRE_SESSION = [requireSessionMiddleware] as const
const REQUIRE_ORG = [requireOrgMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  createBookingPage: REQUIRE_ORG,
  updateBookingPage: REQUIRE_ORG,
  deleteBookingPage: REQUIRE_ORG,
  listMyBookingPages: REQUIRE_ORG,
  getBookingPage: REQUIRE_ORG,
  setHandle: REQUIRE_ORG,
  // Google Calendar connection status/disconnect is about the signed-in user's own account, not
  // the org — a plain session (not necessarily an org membership) is enough.
  getGoogleCalendarStatus: REQUIRE_SESSION,
  disconnectGoogleCalendar: REQUIRE_SESSION,
} as const

export const createBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.createBookingPage)
  .validator(createBookingPageSchema)
  .handler(async ({ data, context }) => {
    return pagesService.createPage(
      getDb(),
      { organizationId: context.org.id, createdBy: context.session.user.id },
      data,
    )
  })

export const updateBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updateBookingPage)
  .validator(updateBookingPageSchema)
  .handler(async ({ data, context }) => {
    const { pageId, ...input } = data
    await pagesService.updatePage(getDb(), pageId, context.org, context.session.user.id, input)
    await notifyPageChanged(pageId)
  })

export const deleteBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.deleteBookingPage)
  .validator(z.object({ pageId: z.string() }))
  .handler(async ({ data, context }) => {
    await pagesService.deletePage(getDb(), data.pageId, context.org, context.session.user.id)
    await notifyPageChanged(data.pageId)
  })

export const listMyBookingPages = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.listMyBookingPages)
  .handler(async ({ context }) => {
    return pagesService.listMyPages(getDb(), context.org.id)
  })

export const getBookingPage = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getBookingPage)
  .validator(z.object({ pageId: z.string() }))
  .handler(async ({ data, context }) => {
    return pagesService.getOwnedPage(getDb(), data.pageId, context.org, context.session.user.id)
  })

export const setHandle = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.setHandle)
  .validator(z.object({ handle: handleSchema }))
  .handler(async ({ data, context }) => {
    // Only the owner may change the org's public slug (spec §1) — everyone's booking pages live
    // under it, so even an admin renaming it would move every member's links too.
    requireOwnerRole(context.org.role)
    await pagesService.setOrgSlug(getDb(), context.org.id, data.handle)
  })

/** Token probe: whether this owner currently has a usable Google Calendar connection, without
 * exposing the token itself to the client. */
export const getGoogleCalendarStatus = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getGoogleCalendarStatus)
  .handler(async ({ context }) => {
    return googleCalendarStatus(context.session.user.id)
  })

export const disconnectGoogleCalendar = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.disconnectGoogleCalendar)
  .handler(async ({ context }) => {
    await pagesService.disconnectGoogleSync(getDb(), context.session.user.id)
  })
