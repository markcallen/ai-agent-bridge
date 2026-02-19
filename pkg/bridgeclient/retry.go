package bridgeclient

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (c *Client) invoke(ctx context.Context, fn func(context.Context) error) error {
	backoff := c.retry.InitialBackoff
	var lastErr error

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		callCtx, cancel := c.ctx(ctx)
		err := fn(callCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetry(err) || attempt == c.retry.MaxAttempts {
			return mapError(err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > c.retry.MaxBackoff {
			backoff = c.retry.MaxBackoff
		}
	}
	return mapError(lastErr)
}

func shouldRetry(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
