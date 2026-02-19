package bridgeclient_test

import (
	"time"

	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func ExampleNew() {
	_, _ = bridgeclient.New(
		bridgeclient.WithTarget("127.0.0.1:9445"),
		bridgeclient.WithTimeout(10*time.Second),
		bridgeclient.WithRetry(bridgeclient.RetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 100 * time.Millisecond,
			MaxBackoff:     2 * time.Second,
		}),
		bridgeclient.WithCursorStore(bridgeclient.NewMemoryCursorStore()),
	)
}
