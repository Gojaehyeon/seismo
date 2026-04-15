package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSnapshotPeakPreservingDecimation(t *testing.T) {
	b := newBus(1, 4, "")

	b.mu.Lock()
	b.window[0] = Reading{T: 1, HX: 1, HY: 0, HZ: 0, M: 0.10}
	b.window[1] = Reading{T: 2, HX: 2, HY: 0, HZ: 0, M: 0.90}
	b.window[2] = Reading{T: 3, HX: 3, HY: 0, HZ: 0, M: 0.20}
	b.window[3] = Reading{T: 4, HX: 4, HY: 0, HZ: 0, M: 0.30}
	b.window[4] = Reading{T: 5, HX: 5, HY: 0, HZ: 0, M: 0.40}
	b.window[5] = Reading{T: 6, HX: 6, HY: 0, HZ: 0, M: 1.10}
	b.filled = 6
	b.head = 6
	b.mu.Unlock()

	snap := b.snapshot(3)
	samples := snap["samples"].([]Reading)

	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}

	if samples[0].T != 1 || samples[0].HX != 1 || samples[0].M != 0.90 {
		t.Fatalf("first bucket = %#v, want first sample coords with peak magnitude 0.90", samples[0])
	}

	if samples[1].T != 4 || samples[1].HX != 4 || samples[1].M != 1.10 {
		t.Fatalf("second bucket = %#v, want first sample coords with peak magnitude 1.10", samples[1])
	}
}

func TestStartHTTPServerReturnsBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := startHTTPServer(ctx, ln.Addr().String(), newBus(1, 4, "")); err == nil {
		t.Fatal("expected bind error, got nil")
	}
}

func TestStartHTTPServerServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := newBus(1, 4, "")

	errCh, err := startHTTPServer(ctx, "127.0.0.1:0", b)
	if err != nil {
		t.Fatalf("startHTTPServer: %v", err)
	}

	// startHTTPServer binds before returning, so cancellation should shut down quickly.
	cancel()

	select {
	case err := <-errCh:
		t.Fatalf("unexpected server error after shutdown: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}
