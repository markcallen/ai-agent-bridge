package redact

import (
	"fmt"
	"regexp"
)

const replacement = "[REDACTED]"

// Redactor applies configured regex patterns to redact sensitive content.
type Redactor struct {
	patterns []*regexp.Regexp
}

// New compiles redact patterns and returns a redactor.
func New(patterns []string) (*Redactor, error) {
	r := &Redactor{
		patterns: make([]*regexp.Regexp, 0, len(patterns)),
	}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile redact pattern %q: %w", pattern, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// Redact returns text with all configured patterns replaced.
func (r *Redactor) Redact(text string) string {
	if r == nil || len(r.patterns) == 0 || text == "" {
		return text
	}
	redacted := text
	for _, re := range r.patterns {
		redacted = re.ReplaceAllString(redacted, replacement)
	}
	return redacted
}
