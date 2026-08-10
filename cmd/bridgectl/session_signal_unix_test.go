//go:build !windows

package main

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestAttachSignalAction(t *testing.T) {
	tests := []struct {
		name     string
		sig      os.Signal
		isWriter bool
		want     attachSignalHandling
	}{
		{
			name:     "observer ignores resize",
			sig:      syscall.SIGWINCH,
			isWriter: false,
			want:     attachSignalIgnore,
		},
		{
			name:     "writer resizes",
			sig:      syscall.SIGWINCH,
			isWriter: true,
			want:     attachSignalResize,
		},
		{
			name:     "interrupt cancels observer",
			sig:      os.Interrupt,
			isWriter: false,
			want:     attachSignalCancel,
		},
		{
			name:     "interrupt cancels writer",
			sig:      os.Interrupt,
			isWriter: true,
			want:     attachSignalCancel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachSignalAction(tt.sig, tt.isWriter); got != tt.want {
				t.Fatalf("attachSignalAction(%v, %v) = %v, want %v", tt.sig, tt.isWriter, got, tt.want)
			}
		})
	}
}

func TestHandleAttachSignalsObserverIgnoresResize(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	sigCh := make(chan os.Signal, 2)
	done := make(chan struct{})
	var resizeCalls atomic.Int32
	var cancelCalls atomic.Int32

	go func() {
		defer close(done)
		handleAttachSignals(ctx, sigCh, false, func() {
			resizeCalls.Add(1)
		}, func() {
			cancelCalls.Add(1)
		})
	}()

	sigCh <- syscall.SIGWINCH
	select {
	case <-done:
		t.Fatal("observer resize signal ended handler")
	case <-time.After(25 * time.Millisecond):
	}
	if got := resizeCalls.Load(); got != 0 {
		t.Fatalf("resizeCalls = %d, want 0", got)
	}
	if got := cancelCalls.Load(); got != 0 {
		t.Fatalf("cancelCalls = %d, want 0", got)
	}

	sigCh <- os.Interrupt
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("interrupt did not end handler")
	}
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("cancelCalls = %d, want 1", got)
	}
}

func TestHandleAttachSignalsWriterResizes(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	sigCh := make(chan os.Signal, 2)
	done := make(chan struct{})
	var resizeCalls atomic.Int32
	var cancelCalls atomic.Int32

	go func() {
		defer close(done)
		handleAttachSignals(ctx, sigCh, true, func() {
			resizeCalls.Add(1)
		}, func() {
			cancelCalls.Add(1)
		})
	}()

	sigCh <- syscall.SIGWINCH
	eventually(t, func() bool { return resizeCalls.Load() == 1 })
	if got := cancelCalls.Load(); got != 0 {
		t.Fatalf("cancelCalls = %d, want 0", got)
	}

	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not end handler")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before deadline")
}
