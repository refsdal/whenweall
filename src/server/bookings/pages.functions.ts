import { createServerFn } from '@tanstack/react-start'
import * as z from 'zod'
import { getDb } from '#/server/db/client'
import { requireSessionMiddleware } from '#/server/auth/middleware'
import { getGoogleAccessToken } from '#/server/google/calendar'
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

export const SERVER_FN_MIDDLEWARE = {
  createBookingPage: REQUIRE_SESSION,
  updateBookingPage: REQUIRE_SESSION,
  deleteBookingPage: REQUIRE_SESSION,
  listMyBookingPages: REQUIRE_SESSION,
  getBookingPage: REQUIRE_SESSION,
  setHandle: REQUIRE_SESSION,
  getGoogleCalendarStatus: REQUIRE_SESSION,
  disconnectGoogleCalendar: REQUIRE_SESSION,
} as const

export const createBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.createBookingPage)
  .validator(createBookingPageSchema)
  .handler(async ({ data, context }) => {
    return pagesService.createPage(getDb(), context.session.user.id, data)
  })

export const updateBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updateBookingPage)
  .validator(updateBookingPageSchema)
  .handler(async ({ data, context }) => {
    const { pageId, ...input } = data
    await pagesService.updatePage(getDb(), pageId, context.session.user.id, input)
    await notifyPageChanged(pageId)
  })

export const deleteBookingPage = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.deleteBookingPage)
  .validator(z.object({ pageId: z.string() }))
  .handler(async ({ data, context }) => {
    await pagesService.deletePage(getDb(), data.pageId, context.session.user.id)
    await notifyPageChanged(data.pageId)
  })

export const listMyBookingPages = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.listMyBookingPages)
  .handler(async ({ context }) => {
    return pagesService.listMyPages(getDb(), context.session.user.id)
  })

export const getBookingPage = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getBookingPage)
  .validator(z.object({ pageId: z.string() }))
  .handler(async ({ data, context }) => {
    return pagesService.getOwnedPage(getDb(), data.pageId, context.session.user.id)
  })

export const setHandle = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.setHandle)
  .validator(z.object({ handle: handleSchema }))
  .handler(async ({ data, context }) => {
    await pagesService.setUserHandle(getDb(), context.session.user.id, data.handle)
  })

/** Token probe: whether this owner currently has a usable Google Calendar connection, without
 * exposing the token itself to the client. */
export const getGoogleCalendarStatus = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getGoogleCalendarStatus)
  .handler(async ({ context }) => {
    const token = await getGoogleAccessToken(context.session.user.id)
    return { connected: token !== null }
  })

export const disconnectGoogleCalendar = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.disconnectGoogleCalendar)
  .handler(async ({ context }) => {
    await pagesService.disconnectGoogleSync(getDb(), context.session.user.id)
  })
