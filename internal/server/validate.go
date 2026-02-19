package server

import (
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxProjectIDLen  = 128
	maxSessionIDLen  = 64
	maxRepoPathLen   = 4096
	maxProviderLen   = 64
	maxAgentOptKey   = 128
	maxAgentOptValue = 4096
	maxListProjectID = 128
)

func validateUUIDField(name, value string) error {
	if err := validateStringField(name, value, maxSessionIDLen, false); err != nil {
		return err
	}
	if _, err := uuid.Parse(value); err != nil {
		return status.Errorf(codes.InvalidArgument, "%s must be a valid UUID", name)
	}
	return nil
}

func validateStringField(name, value string, maxLen int, allowWhitespaceControl bool) error {
	if value == "" {
		return status.Errorf(codes.InvalidArgument, "%s is required", name)
	}
	if !utf8.ValidString(value) {
		return status.Errorf(codes.InvalidArgument, "%s must be valid UTF-8", name)
	}
	if len(value) > maxLen {
		return status.Errorf(codes.InvalidArgument, "%s exceeds max length %d", name, maxLen)
	}
	for _, r := range value {
		if r == 0x7f || r < 0x20 {
			if allowWhitespaceControl && (r == '\n' || r == '\r' || r == '\t') {
				continue
			}
			return status.Errorf(codes.InvalidArgument, "%s contains invalid control characters", name)
		}
	}
	return nil
}

func validateOptionalStringField(name, value string, maxLen int, allowWhitespaceControl bool) error {
	if value == "" {
		return nil
	}
	return validateStringField(name, value, maxLen, allowWhitespaceControl)
}
