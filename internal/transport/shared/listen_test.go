package shared

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"
)

// TestKeepAliveConfigMapping pins the translation from ListenOptions to the net
// package's form, including the two cases that are easy to get wrong: the zero
// value must reproduce net.Listen exactly (nil, not a disabled keepalive), and a
// negative idle must disable probing.
func TestKeepAliveConfigMapping(t *testing.T) {
	cases := []struct {
		name    string
		opts    ListenOptions
		wantNil bool
		enable  bool
		idle    time.Duration
		inter   time.Duration
		count   int
	}{
		{name: "zero keeps net.Listen behaviour", opts: ListenOptions{}, wantNil: true},
		{
			name: "idle only",
			opts: ListenOptions{KeepAliveIdle: 30 * time.Second},
			enable: true, idle: 30 * time.Second,
		},
		{
			name: "negative idle disables probing",
			opts: ListenOptions{KeepAliveIdle: -1},
			enable: false,
		},
		{
			name: "full schedule",
			opts: ListenOptions{
				KeepAliveIdle:     5 * time.Minute,
				KeepAliveInterval: 30 * time.Second,
				KeepAliveCount:    3,
			},
			enable: true, idle: 5 * time.Minute, inter: 30 * time.Second, count: 3,
		},
		{
			name:  "count only keeps the platform idle",
			opts:  ListenOptions{KeepAliveCount: 9},
			enable: true, count: 9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.keepAliveConfig()
			if tc.wantNil {
				if got != nil {
					t.Fatalf("keepAliveConfig() = %+v, want nil so the listener matches net.Listen", got)
				}
				return
			}
			if got == nil {
				t.Fatal("keepAliveConfig() = nil, want a config")
			}
			if got.Enable != tc.enable {
				t.Errorf("Enable = %v, want %v", got.Enable, tc.enable)
			}
			if got.Idle != tc.idle {
				t.Errorf("Idle = %v, want %v", got.Idle, tc.idle)
			}
			if got.Interval != tc.inter {
				t.Errorf("Interval = %v, want %v", got.Interval, tc.inter)
			}
			if got.Count != tc.count {
				t.Errorf("Count = %d, want %d", got.Count, tc.count)
			}
		})
	}
}

// TestListenZeroOptionsBinds pins that adopting this helper changed nothing for
// callers that set no option: a plain listener, nothing more.
func TestListenZeroOptionsBinds(t *testing.T) {
	ln, err := Listen("tcp", "127.0.0.1:0", ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() err = %v, want nil", err)
	}
	defer ln.Close()
	if ln.Addr() == nil {
		t.Fatal("listener has no address")
	}
}

// TestListenKeepAliveAppliesToListener checks the option reaches the socket
// layer rather than being silently dropped: the listener must still accept, and
// a connection through it must be usable with a non-default keepalive schedule.
// The socket-level SO_KEEPALIVE value itself is not portable to assert (Darwin
// uses TCP_KEEPALIVE where Linux uses TCP_KEEPIDLE), so this covers the wiring
// and TestKeepAliveConfigMapping covers the values.
func TestListenKeepAliveAppliesToListener(t *testing.T) {
	ln, err := Listen("tcp", "127.0.0.1:0", ListenOptions{
		KeepAliveIdle:     3 * time.Second,
		KeepAliveInterval: time.Second,
		KeepAliveCount:    2,
	})
	if err != nil {
		t.Fatalf("Listen() err = %v, want nil", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial() err = %v", err)
	}
	defer c.Close()
	srv, ok := <-accepted
	if !ok {
		t.Fatal("Accept() failed")
	}
	defer srv.Close()

	// The connection must be a working one, not merely accepted: a keepalive
	// misconfiguration (for example Enable with a zero Idle) can make the
	// handshake fail instead of being ignored.
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("Write() err = %v", err)
	}
	buf := make([]byte, 4)
	if err := srv.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Read(buf); err != nil {
		t.Fatalf("Read() err = %v", err)
	}
}

// TestListenReusePort verifies port sharing where the platform has it, and
// verifies the explicit failure where it does not.
//
// The positive half only executes on Linux and Darwin: SO_REUSEPORT is not
// available on Windows, and quietly succeeding there would be the failure mode
// the error exists to prevent.
func TestListenReusePort(t *testing.T) {
	first, err := Listen("tcp", "127.0.0.1:0", ListenOptions{ReusePort: true})

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrReusePortUnsupported) {
			if first != nil {
				first.Close()
			}
			t.Fatalf("Listen() on %s err = %v, want ErrReusePortUnsupported", runtime.GOOS, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("Listen(ReusePort) err = %v, want nil", err)
	}
	defer first.Close()

	addr := first.Addr().String()
	second, err := Listen("tcp", addr, ListenOptions{ReusePort: true})
	if err != nil {
		t.Fatalf("second Listen(%s, ReusePort) err = %v, want nil: both sockets must set SO_REUSEPORT", addr, err)
	}
	defer second.Close()

	// Without the option the bind must still fail: SO_REUSEPORT is per-socket,
	// not a property of the address, so a listener that did not ask for it
	// cannot join.
	plain, err := Listen("tcp", addr, ListenOptions{})
	if err == nil {
		plain.Close()
		t.Fatalf("plain Listen(%s) succeeded, want an address-in-use error", addr)
	}
}
