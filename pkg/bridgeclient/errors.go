package bridgeclient

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrSessionAlreadyExists = errors.New("session already exists")
	ErrProviderUnavailable  = errors.New("provider unavailable")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrPermissionDenied     = errors.New("permission denied")
	ErrInputTooLarge        = errors.New("input too large")
	ErrSessionLimitReached  = errors.New("session limit reached")
)

// mapError converts gRPC status errors to typed SDK errors.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return ErrSessionNotFound
	case codes.AlreadyExists:
		return ErrSessionAlreadyExists
	case codes.Unauthenticated:
		return ErrUnauthorized
	case codes.PermissionDenied:
		return ErrPermissionDenied
	case codes.ResourceExhausted:
		return ErrSessionLimitReached
	case codes.Unavailable:
		return ErrProviderUnavailable
	default:
		return err
	}
}
