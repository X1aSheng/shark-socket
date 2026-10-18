package coap

import (
	"testing"
	"time"
)

// TestInitialRetransmitTimeoutBracket pins the RFC 7252 section 4.2 range for
// the first timeout: ACK_TIMEOUT <= T <= ACK_TIMEOUT*ACK_RANDOM_FACTOR.
func TestInitialRetransmitTimeoutBracket(t *testing.T) {
	lo := initialRetransmitTimeout(0)
	hi := initialRetransmitTimeout(ackTimeoutSpread - time.Nanosecond)
	if lo != ackTimeout {
		t.Errorf("initial timeout with a zero draw = %v, want %v", lo, ackTimeout)
	}
	if hi != ackTimeoutUpper-time.Nanosecond {
		t.Errorf("initial timeout with the largest draw = %v, want %v", hi, ackTimeoutUpper-time.Nanosecond)
	}
	if hi >= ackTimeoutUpper {
		t.Errorf("initial timeout = %v, must stay below ACK_TIMEOUT*ACK_RANDOM_FACTOR = %v",
			hi, ackTimeoutUpper)
	}
}

// TestInitialRetransmitTimeoutIsJittered is the thundering-herd guard: if every
// server drew the same timeout, every device that lost connectivity at the same
// moment would retransmit in lockstep and re-congest the link together.
func TestInitialRetransmitTimeoutIsJittered(t *testing.T) {
	seen := make(map[time.Duration]struct{})
	for i := 0; i < 64; i++ {
		seen[initialRetransmitTimeout(defaultJitter())] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("64 draws produced %d distinct timeouts, want a spread over [2s, 3s)", len(seen))
	}
}

// TestRetransmitScheduleMatchesRFC7252Table walks one CON message through the
// whole schedule with a synthetic clock and compares against the table in
// RFC 7252 section 4.2.
//
// The table is per-interval, not per-cumulative-time:
//
//	Retransmission  Timeout  Interval since   Total elapsed
//	#               (s)      previous TX (s)  (s)
//	0               -        -                0
//	1               2        2-3              2-3
//	2               4        4-6              6-9
//	3               8        8-12             14-21
//	4               16       16-24            30-45
//
// so the assertion is: interval_n in [2^(n-1)*ACK_TIMEOUT,
// 2^(n-1)*ACK_TIMEOUT*ACK_RANDOM_FACTOR) and elapsed_n the sum of them. The
// acceptance criterion first written in COMPARISON-261018-OSS-PEERS.md
// ("n-th retransmission in [2^n*2, 2^n*3] seconds") was wrong on both counts:
// it used cumulative time for a per-interval number, and 2^n where the table
// has 2^(n-1).
func TestRetransmitScheduleMatchesRFC7252Table(t *testing.T) {
	// Drive the schedule with the extreme draws to bound both ends: the
	// smallest initial timeout gives the earliest possible transmissions, the
	// largest the latest.
	for _, tc := range []struct {
		name  string
		start time.Duration
	}{
		{"earliest", 0},
		{"latest", ackTimeoutSpread - time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
			first := initialRetransmitTimeout(tc.start)
			pn := pendingNotify{timeout: first, deadline: t0.Add(first)}

			// Jump straight to each deadline: this asserts the schedule
			// arithmetic itself, with no scheduler granularity mixed in.
			var sent []time.Duration
			for i := 0; i < maxRetransmit; i++ {
				now := pn.deadline
				if got := pn.decide(now); got != retransmitSend {
					t.Fatalf("decide at the deadline = %v, want retransmitSend", got)
				}
				sent = append(sent, now.Sub(t0))
				pn = pn.advance(now)
			}
			gaveUp := pn.deadline.Sub(t0)
			if got := pn.decide(pn.deadline); got != retransmitGiveUp {
				t.Fatalf("decide at the final deadline = %v, want retransmitGiveUp", got)
			}

			// Interval since the previous transmission: 2^(n-1) * [2, 3).
			prev := time.Duration(0)
			for n, at := range sent {
				interval := at - prev
				lo := ackTimeout << n
				hi := (ackTimeout + ackTimeoutSpread) << n
				if interval < lo || interval >= hi {
					t.Errorf("interval before retransmission %d = %v, want [%v, %v)", n+1, interval, lo, hi)
				}
				prev = at
			}

			// Total elapsed: [2,3) + [4,6) + [8,12) + [16,24) = [30, 45).
			if last := sent[len(sent)-1]; last < 30*time.Second || last >= 45*time.Second {
				t.Errorf("4th retransmission at %v, want [30s, 45s)", last)
			}
			// MAX_TRANSMIT_WAIT: give up 16x the initial timeout after the last
			// transmission, i.e. [62, 93) in total.
			if gaveUp < 62*time.Second || gaveUp >= 93*time.Second {
				t.Errorf("gave up at %v, want [62s, 93s) (MAX_TRANSMIT_WAIT)", gaveUp)
			}
			if n := pn.attempts; n != maxRetransmit {
				t.Errorf("attempts = %d, want %d", n, maxRetransmit)
			}
		})
	}
}

// TestRetransmitTickLatencyDoesNotAccumulate pins what happens when the
// scheduler runs on a coarse tick instead of an exact deadline: every
// transmission is late by less than one tick, but the lateness must not add up
// across retransmissions, because advance() derives the next deadline from the
// previous deadline rather than from the moment the tick happened to fire. With
// four retransmissions of a dt-late schedule the drift would otherwise reach
// 4*dt and eat into the last, longest interval.
func TestRetransmitTickLatencyDoesNotAccumulate(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	pn := pendingNotify{timeout: ackTimeout, deadline: t0.Add(ackTimeout)}

	var sent []time.Duration
	now := t0
	for len(sent) < maxRetransmit {
		now = now.Add(retransmitTick)
		if pn.decide(now) != retransmitSend {
			continue
		}
		sent = append(sent, now.Sub(t0))
		pn = pn.advance(now)
	}

	// Recompute the exact schedule and compare latency per transmission.
	cumulative := time.Duration(0)
	interval := time.Duration(ackTimeout)
	for n, got := range sent {
		cumulative += interval
		interval *= 2
		late := got - cumulative
		if late < 0 || late >= 2*retransmitTick {
			t.Errorf("retransmission %d at %v, expected %v, latency %v outside [0, %v)",
				n+1, got, cumulative, late, 2*retransmitTick)
		}
	}
}

// TestRetransmitNeverFiresBeforeAckTimeout is the regression test for the
// defect this schedule replaced: a global 2s ticker retransmitted whatever was
// pending, so a message tracked 1ms before a tick was retransmitted 1ms later
// instead of after ACK_TIMEOUT - which also let a peer with a slow ACK path be
// flooded at tick rate rather than at the prescribed interval.
func TestRetransmitNeverFiresBeforeAckTimeout(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for draw := time.Duration(0); draw < ackTimeoutSpread; draw += 50 * time.Millisecond {
		first := initialRetransmitTimeout(draw)
		pn := pendingNotify{timeout: first, deadline: t0.Add(first)}
		// Anything strictly before ACK_TIMEOUT must not be due, no matter how
		// close to the deadline it is.
		for probe := time.Duration(0); probe < ackTimeout; probe += 10 * time.Millisecond {
			if got := pn.decide(t0.Add(probe)); got != retransmitWait {
				t.Fatalf("draw %v: decide at +%v = %v, want retransmitWait", draw, probe, got)
			}
		}
		if got := pn.decide(t0.Add(first)); got != retransmitSend {
			t.Fatalf("draw %v: decide at +%v (the deadline) = %v, want retransmitSend", draw, first, got)
		}
	}
}

// TestRetransmitLateTickDoesNotBurst pins the catch-up rule: a scheduler tick
// that arrives far later than the deadline must produce one transmission and
// then wait a full doubled interval, not a burst of back-to-back transmissions
// to "repay" the missed schedule.
func TestRetransmitLateTickDoesNotBurst(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	first := ackTimeout
	pn := pendingNotify{timeout: first, deadline: t0.Add(first)}

	// The tick arrives 30s late.
	late := t0.Add(30 * time.Second)
	if got := pn.decide(late); got != retransmitSend {
		t.Fatalf("decide at a late tick = %v, want retransmitSend", got)
	}
	pn = pn.advance(late)
	if want := late.Add(2 * first); !pn.deadline.Equal(want) {
		t.Errorf("next deadline = %v, want %v (measured from the tick, not the missed deadline)",
			pn.deadline, want)
	}
	if got := pn.decide(late.Add(100 * time.Millisecond)); got != retransmitWait {
		t.Errorf("decide right after a late tick = %v, want retransmitWait", got)
	}
}

// TestRetransmitGiveUpWaitsFinalTimeout pins the ordering between MAX_RETRANSMIT
// and the deadline: exhausting the retransmission count does not abandon the
// exchange immediately, it waits one more timeout for a late ACK.
func TestRetransmitGiveUpWaitsFinalTimeout(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	pn := pendingNotify{attempts: maxRetransmit, timeout: 16 * time.Second, deadline: t0.Add(16 * time.Second)}
	if got := pn.decide(t0.Add(15 * time.Second)); got != retransmitWait {
		t.Errorf("decide 1s before the final deadline = %v, want retransmitWait", got)
	}
	if got := pn.decide(t0.Add(16 * time.Second)); got != retransmitGiveUp {
		t.Errorf("decide at the final deadline = %v, want retransmitGiveUp", got)
	}
}
