package shared

import (
	"errors"
	"net"
	"syscall"
)

// AcceptErrorKind classifies an error returned by net.Listener.Accept.
//
// The three cases need three different reactions, and the old code had one: a
// fixed 100ms sleep and a log line for everything. That is wrong in both
// directions - on EMFILE it turns one full file-descriptor table into ten log
// lines a second plus an accept path that is asleep most of the time, and on a
// per-connection error (a client that reset between SYN and accept) it delays
// every other connection by 100ms for no reason.
type AcceptErrorKind uint8

const (
	// AcceptErrorClosed means the listener is gone: stop accepting and let the
	// goroutine return. Retrying would spin.
	AcceptErrorClosed AcceptErrorKind = iota
	// AcceptErrorResource means the process or the machine ran out of a kernel
	// resource (file descriptors, memory). The listener is still valid, so the
	// loop must back off and try again - and it must be visible in metrics,
	// because "the server is out of descriptors" is otherwise indistinguishable
	// from "the server is idle".
	AcceptErrorResource
	// AcceptErrorTemporary is everything else: a failure that concerns the one
	// connection that could not be accepted. Retrying immediately is correct,
	// but a bounded backoff keeps a persistently broken listener from spinning.
	AcceptErrorTemporary
)

func (k AcceptErrorKind) String() string {
	switch k {
	case AcceptErrorClosed:
		return "closed"
	case AcceptErrorResource:
		return "resource"
	case AcceptErrorTemporary:
		return "temporary"
	}
	return "unknown"
}

// ClassifyAcceptError decides how an accept loop should react to err.
func ClassifyAcceptError(err error) AcceptErrorKind {
	if err == nil {
		return AcceptErrorTemporary
	}
	// net.ErrClosed covers both a listener closed by StopAccept and a wrapped
	// one (the PROXY protocol listener closes the inner listener).
	if errors.Is(err, net.ErrClosed) {
		return AcceptErrorClosed
	}
	if IsResourceExhaustion(err) {
		return AcceptErrorResource
	}
	return AcceptErrorTemporary
}

// IsResourceExhaustion reports whether err is the kernel refusing to hand out a
// resource rather than a per-connection failure.
//
// EMFILE is the process limit and ENFILE the system-wide one; ENOMEM covers the
// socket buffers. All three mean "same request, later" rather than "this
// connection was bad", which is the distinction the caller needs.
func IsResourceExhaustion(err error) bool {
	return errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOMEM)
}
