package bridge

import "errors"

var (
	ErrInvalidArgument            = errors.New("invalid argument")
	ErrSessionNotFound            = errors.New("session not found")
	ErrSessionAlreadyExists       = errors.New("session already exists")
	ErrSessionNotRunning          = errors.New("session not running")
	ErrSessionRecoveryUnavailable = errors.New("session recovered without live transport")
	ErrSessionAlreadyAttached     = errors.New("session already has an attached client")
	ErrClientNotAttached          = errors.New("client is not attached")
	ErrClientMismatch             = errors.New("client does not own attached session")
	ErrProviderUnavailable        = errors.New("provider unavailable")
	ErrSessionLimitReached        = errors.New("session limit reached")
	ErrInputTooLarge              = errors.New("input too large")
)
