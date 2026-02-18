package bridge

import "errors"

var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrSessionNotFound      = errors.New("session not found")
	ErrSessionAlreadyExists = errors.New("session already exists")
	ErrSessionNotRunning    = errors.New("session not running")
	ErrProviderUnavailable  = errors.New("provider unavailable")
	ErrSessionLimitReached  = errors.New("session limit reached")
	ErrInputTooLarge        = errors.New("input too large")
)
