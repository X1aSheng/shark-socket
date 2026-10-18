package coap

import (
	"math/rand"
	"time"
)

// RFC 7252 section 4.2 transmission parameters.
//
// These are not tuning knobs: the specification prescribes them so that a
// client and a server back off compatibly. Changing them changes interop
// behaviour, not just latency.
const (
	// ackTimeout is ACK_TIMEOUT: the lower bound of the first retransmission
	// timeout.
	ackTimeout = 2 * time.Second
	// ackRandomFactor is ACK_RANDOM_FACTOR. The first timeout is drawn from
	// [ackTimeout, ackTimeout*ackRandomFactor), i.e. [2s, 3s).
	ackRandomFactor = 1.5
	// ackTimeoutSpread is the width of that window, ackTimeout*(factor-1).
	// Spelled as a division so it stays exact: 2s*0.5 == 1s with no float
	// rounding to reason about.
	ackTimeoutSpread = ackTimeout / 2
	// maxRetransmit is MAX_RETRANSMIT: a CON message is transmitted once and
	// then retransmitted at most four times. With the schedule below that puts
	// the four retransmissions at 2-3s, 6-9s, 14-21s and 30-45s after the
	// first transmission, matching the table in RFC 7252 section 4.2, and
	// gives up at 62-93s (MAX_TRANSMIT_WAIT).
	maxRetransmit = 4
	// retransmitTick is the resolution of the scheduler, not the interval
	// between transmissions. Each entry fires on its own deadline; the tick
	// only bounds how late a deadline can be noticed.
	retransmitTick = 250 * time.Millisecond
)

// ackTimeoutUpper is ACK_TIMEOUT*ACK_RANDOM_FACTOR, the exclusive upper bound of
// the first retransmission timeout. Derived from ackRandomFactor so the
// specification value appears exactly once; the float multiply is exact here
// (3e9 ns is representable).
var ackTimeoutUpper = time.Duration(float64(ackTimeout) * ackRandomFactor)

// initialRetransmitTimeout maps a draw in [0, ackTimeoutSpread) onto the first
// (re)transmission timeout, which RFC 7252 section 4.2 requires to fall inside
// [ACK_TIMEOUT, ACK_TIMEOUT*ACK_RANDOM_FACTOR).
func initialRetransmitTimeout(draw time.Duration) time.Duration {
	if draw < 0 {
		draw = 0
	}
	return ackTimeout + draw%ackTimeoutSpread
}

// defaultJitter draws the initial-timeout jitter from the global random source.
// It only ever runs once per tracked CON message, so the lock inside math/rand
// is not on any hot path.
func defaultJitter() time.Duration {
	return time.Duration(rand.Int63n(int64(ackTimeoutSpread)))
}

// retransmitAction is what the scheduler does with one pending entry at a given
// instant.
type retransmitAction int

const (
	// retransmitWait means the current timeout has not elapsed yet.
	retransmitWait retransmitAction = iota
	// retransmitSend means transmit now.
	retransmitSend
	// retransmitGiveUp means MAX_RETRANSMIT is exhausted and the final timeout
	// elapsed without an ACK.
	retransmitGiveUp
)

// decide reports what to do with pn at now.
//
// It is deliberately pure. The schedule is the part that has to be provably
// right, and asserting it should not need a socket, a ticker or the wall clock:
// the fixed 2s-interval timer this replaced could not be tested for the one
// property that matters, namely that a retransmission never precedes
// ACK_TIMEOUT.
func (pn pendingNotify) decide(now time.Time) retransmitAction {
	if now.Before(pn.deadline) {
		return retransmitWait
	}
	if pn.attempts >= maxRetransmit {
		return retransmitGiveUp
	}
	return retransmitSend
}

// advance returns the entry as it must look after a transmission at now: the
// timeout doubles (RFC 7252 section 4.2) and the next deadline is derived from
// the current one, so tick latency does not accumulate across retransmissions.
//
// A tick that arrives long after a deadline must not be repaid with a burst of
// back-to-back transmissions: when the derived deadline is already in the past,
// the new interval is measured from now instead.
func (pn pendingNotify) advance(now time.Time) pendingNotify {
	pn.attempts++
	pn.timeout *= 2
	next := pn.deadline.Add(pn.timeout)
	if next.Before(now) {
		next = now.Add(pn.timeout)
	}
	pn.deadline = next
	return pn
}
