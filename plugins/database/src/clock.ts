// When writes lock again, on this computer's clock.
//
// The helper says when in milliseconds since 1970 by the box's clock
// (writes_until), and a box's clock is not this computer's: a box a minute
// ahead would show a minute more than there is, and one behind would show
// writes locked while they are on. What is known for certain is that writes
// last 15 minutes from the moment they were turned on, so the countdown
// starts from when that answer arrived, and the difference between the two
// clocks, learned then, is kept for reading the same box later.

// How long writes stay on, which the helper decides; said before unlocking.
export const WRITES_FOR_MS = 15 * 60_000;

// skewOf is how far the box's clock is ahead of this one, worked out from
// the answer to turning writes on: it said writes_until, and that is 15
// minutes after it was asked.
export function skewOf(writesUntil: number, arrivedAt: number): number {
  return writesUntil - (arrivedAt + WRITES_FOR_MS);
}

// untilHere is when a connection's writes_until falls on this clock, given
// what is known of the box's (0 when nothing is). It is never more than 15
// minutes from now, whatever the clocks say: writes cannot last longer.
export function untilHere(writesUntil: number, skew: number, now: number): number {
  return Math.min(writesUntil - skew, now + WRITES_FOR_MS);
}
