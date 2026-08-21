import handler from '@tanstack/react-start/server-entry'
import { paraglideMiddleware } from './paraglide/server'

export { PollRoom } from './do/PollRoom'
export { BookingRoom } from './do/BookingRoom'

export default {
  fetch(request: Request): Promise<Response> {
    return paraglideMiddleware(request, ({ request: localizedRequest }) =>
      handler.fetch(localizedRequest),
    )
  },
}
