export { PollRoom } from '#/do/PollRoom'
export { BookingRoom } from '#/do/BookingRoom'
export { StatsRoom } from '#/do/StatsRoom'
export { RateLimitRoom } from '#/do/RateLimitRoom'

export default { fetch: () => new Response('test worker', { status: 404 }) }
