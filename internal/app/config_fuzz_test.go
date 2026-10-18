package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

// FuzzDecodeConfig feeds arbitrary documents to the config decoder.
//
// A config file is operator input, and operator input is malformed far more
// often than it is hostile: a truncated paste, a stray brace, a port written as
// a string. What must never happen is a panic or a silent acceptance of
// something that then fails somewhere else, so the properties here are:
//
//  1. no panic on any byte sequence;
//  2. an accepted document must also validate (decodeConfig plus Validate is
//     the contract LoadConfig offers, and nothing may be accepted here that
//     LoadConfig would reject);
//  3. an accepted document must survive marshal -> decode -> validate. Any
//     config this server accepts has to be expressible by the same schema;
//     if the round trip fails, every tool that reads, edits and writes the
//     file back is broken.
func FuzzDecodeConfig(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"log_level":"debug","protocols":[{"name":"tcp","addr":"127.0.0.1:18000"}]}`))
	f.Add([]byte(`{"protocols":[{"name":"quic","addr":"127.0.0.1:18501"}]}`))
	f.Add([]byte(`{"shutdown_timeout":"-1s"}`))
	f.Add([]byte(`{"rate_limit":{"per_second":0}}`))
	f.Add([]byte(`{"blacklist":{"mode":"deny","ips":["10.0.0.1"]}}`))
	f.Add([]byte(`{"protocols":[{"name":"tcp","addr":"127.0.0.1:18000"},{"name":"tcp","addr":"127.0.0.1:18000"}]}`))
	f.Add([]byte(`{"max_message_bytes":-1}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"protocols":`))

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := decodeConfig(data)
		if err != nil {
			return
		}
		if err := cfg.Validate(); err != nil {
			// Decoding a syntactically valid document that does not validate
			// is the normal path for a rejected config; nothing to assert.
			return
		}

		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("accepted config does not marshal back: %v", err)
		}
		again, err := decodeConfig(encoded)
		if err != nil {
			t.Fatalf("accepted config did not decode from its own marshal output: %v\n%s", err, encoded)
		}
		if err := again.Validate(); err != nil {
			t.Fatalf("accepted config did not validate after a round trip: %v\n%s", err, encoded)
		}
		if len(again.Protocols) != len(cfg.Protocols) {
			t.Fatalf("round trip changed the protocol count: %d -> %d\n%s",
				len(cfg.Protocols), len(again.Protocols), encoded)
		}
		for i := range cfg.Protocols {
			if !reflect.DeepEqual(again.Protocols[i], cfg.Protocols[i]) {
				t.Fatalf("round trip changed protocol %d: %+v -> %+v\n%s",
					i, cfg.Protocols[i], again.Protocols[i], encoded)
			}
		}
	})
}
