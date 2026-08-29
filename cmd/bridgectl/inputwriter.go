package main

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
)

// inputWriterClient is the subset of the gRPC client needed by inputWriter.
type inputWriterClient interface {
	WriteInput(ctx context.Context, req *bridgev1.WriteInputRequest) (*bridgev1.WriteInputResponse, error)
}

// inputWriterConfig configures an inputWriter.
type inputWriterConfig struct {
	// Reader is the source of raw input (typically os.Stdin).
	Reader io.Reader
	// Client sends coalesced data to the bridge.
	Client inputWriterClient
	// SessionID and ClientID for the WriteInput RPC.
	SessionID string
	ClientID  string
	// DetachKey, if non-zero, causes the writer to stop when this byte is read.
	// OnDetach is called before returning when the detach key is encountered.
	DetachKey byte
	OnDetach  func()
	// OnReadError is called when the reader returns an error (other than the
	// detach-key path). The writer goroutine exits after calling it.
	OnReadError func(err error)

	// FlushInterval controls how long the writer waits after the last byte
	// before flushing. Defaults to 20ms.
	FlushInterval time.Duration
}

// inputWriter coalesces individual keystrokes into batched WriteInput RPCs.
// Instead of sending one RPC per os.Stdin.Read (often a single byte in raw
// terminal mode), it buffers input and flushes either when no new data arrives
// for FlushInterval or when the buffer reaches 4 KiB.
type inputWriter struct {
	cfg inputWriterConfig

	mu  sync.Mutex
	buf bytes.Buffer
}

const (
	defaultFlushInterval = 20 * time.Millisecond
	maxBatchSize         = 4096
)

// Run starts reading from cfg.Reader and coalescing writes. It blocks until
// the reader returns an error or the detach key is encountered. It is intended
// to be called in a goroutine.
func (w *inputWriter) Run() {
	flushInterval := w.cfg.FlushInterval
	if flushInterval == 0 {
		flushInterval = defaultFlushInterval
	}

	readCh := make(chan []byte, 32)
	errCh := make(chan error, 1)

	// Reader goroutine: reads raw bytes and sends them to readCh.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, readErr := w.cfg.Reader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				readCh <- data
			}
			if readErr != nil {
				errCh <- readErr
				return
			}
		}
	}()

	timer := time.NewTimer(flushInterval)
	timer.Stop()

	for {
		select {
		case data := <-readCh:
			// Check for detach key before buffering.
			if w.cfg.DetachKey != 0 {
				if idx := bytes.IndexByte(data, w.cfg.DetachKey); idx >= 0 {
					// Buffer everything before the detach key, flush, then detach.
					if idx > 0 {
						w.mu.Lock()
						w.buf.Write(data[:idx])
						w.mu.Unlock()
					}
					w.flush()
					if w.cfg.OnDetach != nil {
						w.cfg.OnDetach()
					}
					return
				}
			}

			w.mu.Lock()
			w.buf.Write(data)
			size := w.buf.Len()
			w.mu.Unlock()

			if size >= maxBatchSize {
				timer.Stop()
				w.flush()
			} else {
				// Reset timer to coalesce more bytes within the interval.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(flushInterval)
			}

		case <-timer.C:
			w.flush()

		case err := <-errCh:
			// Drain any remaining data from readCh.
		drain:
			for {
				select {
				case data := <-readCh:
					w.mu.Lock()
					w.buf.Write(data)
					w.mu.Unlock()
				default:
					break drain
				}
			}
			w.flush()
			if w.cfg.OnReadError != nil {
				w.cfg.OnReadError(err)
			}
			return
		}
	}
}

func (w *inputWriter) flush() {
	w.mu.Lock()
	if w.buf.Len() == 0 {
		w.mu.Unlock()
		return
	}
	data := make([]byte, w.buf.Len())
	copy(data, w.buf.Bytes())
	w.buf.Reset()
	w.mu.Unlock()

	_, _ = w.cfg.Client.WriteInput(context.Background(), &bridgev1.WriteInputRequest{
		SessionId: w.cfg.SessionID,
		ClientId:  w.cfg.ClientID,
		Data:      data,
	})
}
