package httpapi

import (
	"sync"
	"sync/atomic"
)

// Drain is the server's shutdown sequence as its handlers see it. Begin
// fails readiness while the process keeps serving, so load balancers stop
// sending new requests before the listener closes. EndStreams ends the
// indefinite event streams once shutdown starts, so the wait for in-flight
// requests is bounded by real work (an agent's 30s long-poll) rather than
// by a browser's open stream, which reconnects to another replica.
type Drain struct {
	draining atomic.Bool
	streams  chan struct{}
	once     sync.Once
}

func NewDrain() *Drain {
	return &Drain{streams: make(chan struct{})}
}

// Begin makes readiness report not ready from now on.
func (d *Drain) Begin() {
	d.draining.Store(true)
}

func (d *Drain) Draining() bool {
	return d.draining.Load()
}

// EndStreams closes every open event stream. A stream opened afterwards
// returns at once, and the client reconnects to another replica.
func (d *Drain) EndStreams() {
	d.once.Do(func() { close(d.streams) })
}

// StreamsEnded closes when EndStreams runs; an event stream selects on it
// and returns.
func (d *Drain) StreamsEnded() <-chan struct{} {
	return d.streams
}
