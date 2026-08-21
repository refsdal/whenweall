import handler from '@tanstack/react-start/server-entry'

export default {
  fetch(request: Request): Promise<Response> {
    return Promise.resolve(handler.fetch(request))
  },
}

export { PollRoom } from './do/PollRoom'
