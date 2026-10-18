package core

import "testing"

// TestProtocolLabelMatchesEveryProtocol guards the pre-built pairs against a
// typo: a label value that does not equal the protocol would split one
// transport's series in two, and nothing else in the codebase compares them.
//
// A new Protocol constant does not need a new case here (the fallback builds a
// fresh pair), but it does need to be added to this list -- an unlisted value
// would simply go untested.
func TestProtocolLabelMatchesEveryProtocol(t *testing.T) {
	all := []Protocol{
		ProtocolTCP, ProtocolUDP, ProtocolHTTP, ProtocolWS,
		ProtocolCoAP, ProtocolQUIC, ProtocolGRPCWeb, ProtocolCustom,
	}
	for _, p := range all {
		got := ProtocolLabel(p)
		if len(got) != 2 || got[0] != "protocol" || got[1] != string(p) {
			t.Fatalf("ProtocolLabel(%q) = %v, want [protocol %q]", p, got, p)
		}
	}
}

// TestProtocolLabelFallbackForUnknownProtocol pins the behaviour for a
// caller-defined protocol: a correct pair, not a panic and not a shared slice
// that would alias another protocol's series.
func TestProtocolLabelFallbackForUnknownProtocol(t *testing.T) {
	custom := Protocol("carrier-pigeon")
	got := ProtocolLabel(custom)
	if len(got) != 2 || got[0] != "protocol" || got[1] != "carrier-pigeon" {
		t.Fatalf("ProtocolLabel(%q) = %v", custom, got)
	}
	if other := ProtocolLabel(Protocol("smoke-signal")); other[1] != "smoke-signal" {
		t.Fatalf("fallback aliased a previous call: %v", other)
	}
}

// TestProtocolLabelIsStable checks the pair is reusable across messages: the
// hot path hands this slice to the metrics backend on every message, so a
// (hypothetical) implementation that rebuilt it would allocate there.
func TestProtocolLabelIsStable(t *testing.T) {
	first := ProtocolLabel(ProtocolTCP)
	second := ProtocolLabel(ProtocolTCP)
	if &first[0] != &second[0] {
		t.Fatal("ProtocolLabel rebuilt the pair for a known protocol; the hot path expects a shared one")
	}
}
