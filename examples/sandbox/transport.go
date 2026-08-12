package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/trentkm/spanreed"
)

type transportSnapshot struct {
	SeedBytes     int64
	OutputBytes   int64
	OutputChunks  int64
	InputBytes    int64
	Writes        int64
	Resizes       int64
	LastResize    [2]int
	LastError     string
	LastActivity  time.Time
	StreamClosed  bool
	TransportDone bool
}

// observedTransport keeps the sandbox's diagnostics outside spanreed itself.
// It forwards every operation unchanged while recording enough information to
// tell a rendering problem from a quiet or failed transport.
type observedTransport struct {
	inner  spanreed.Transport
	seed   []byte
	output chan []byte
	debug  *debugLog

	closeOnce sync.Once
	mu        sync.Mutex
	stats     transportSnapshot
}

func observeTransport(inner spanreed.Transport, logs ...*debugLog) *observedTransport {
	seed := append([]byte(nil), inner.Seed()...)
	var debug *debugLog
	if len(logs) > 0 {
		debug = logs[0]
	}
	transport := &observedTransport{
		inner:  inner,
		seed:   seed,
		output: make(chan []byte),
		debug:  debug,
		stats: transportSnapshot{
			SeedBytes:    int64(len(seed)),
			LastActivity: time.Now(),
		},
	}
	debug.Printf("transport_attach seed_bytes=%d", len(seed))
	go transport.forwardOutput()
	return transport
}

func (t *observedTransport) forwardOutput() {
	defer t.debug.Recover("observedTransport.forwardOutput")
	defer close(t.output)
	for chunk := range t.inner.Output() {
		t.mu.Lock()
		t.stats.OutputBytes += int64(len(chunk))
		t.stats.OutputChunks++
		t.stats.LastActivity = time.Now()
		t.mu.Unlock()
		t.output <- chunk
	}
	t.mu.Lock()
	t.stats.StreamClosed = true
	t.stats.LastActivity = time.Now()
	t.mu.Unlock()
	t.debug.Printf("transport_stream_closed")
}

func (t *observedTransport) Seed() []byte {
	return t.seed
}

func (t *observedTransport) Output() <-chan []byte {
	return t.output
}

func (t *observedTransport) Write(p []byte) error {
	err := t.inner.Write(p)
	t.mu.Lock()
	t.stats.Writes++
	t.stats.InputBytes += int64(len(p))
	t.stats.LastActivity = time.Now()
	if err != nil {
		t.stats.LastError = fmt.Sprintf("write: %v", err)
	}
	t.mu.Unlock()
	if err != nil {
		t.debug.Printf("transport_write_error bytes=%d error=%q", len(p), err)
	}
	return err
}

func (t *observedTransport) Resize(ctx context.Context, cols, rows int) error {
	started := time.Now()
	err := t.inner.Resize(ctx, cols, rows)
	t.mu.Lock()
	t.stats.Resizes++
	t.stats.LastResize = [2]int{cols, rows}
	t.stats.LastActivity = time.Now()
	if err != nil {
		t.stats.LastError = fmt.Sprintf("resize: %v", err)
	}
	t.mu.Unlock()
	t.debug.Printf(
		"transport_resize cols=%d rows=%d duration=%s error=%v",
		cols,
		rows,
		time.Since(started).Round(time.Microsecond),
		err,
	)
	return err
}

func (t *observedTransport) Close() {
	t.closeOnce.Do(func() {
		t.debug.Printf("transport_close")
		t.inner.Close()
		t.mu.Lock()
		t.stats.TransportDone = true
		t.stats.LastActivity = time.Now()
		t.mu.Unlock()
	})
}

func (t *observedTransport) Snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}
