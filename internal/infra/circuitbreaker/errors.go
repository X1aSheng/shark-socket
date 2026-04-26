package circuitbreaker

import "errors"

var ErrCircuitOpen = errors.New("circuitbreaker: circuit is open")
