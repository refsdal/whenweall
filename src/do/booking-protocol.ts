/** Wire protocol shared between the BookingRoom durable object and the browser. Browser-safe: no
 * server imports. Booking pages have no presence UI (unlike polls), so this is deliberately just
 * the one event a live booking/manage page needs: "something about this page's bookings changed,
 * re-fetch availability/booking state." */
export type BookingRoomEvent = { type: 'page.changed' }
