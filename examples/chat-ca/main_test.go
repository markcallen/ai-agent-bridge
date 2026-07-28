package main

import (
	"bytes"
	"testing"
)

func TestBytesTrimSpace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{name: "trims surrounding whitespace", in: []byte(" \n\t hello world \r\n"), want: []byte("hello world")},
		{name: "preserves interior whitespace", in: []byte("  hello \t world  "), want: []byte("hello \t world")},
		{name: "all whitespace becomes empty", in: []byte("\r\n\t "), want: []byte{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := bytesTrimSpace(tc.in)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("bytesTrimSpace(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeTTYInput(t *testing.T) {
	t.Parallel()

	input := []byte("line one\nline two\r\nline three")
	got := normalizeTTYInput(input)
	want := []byte("line one\rline two\r\rline three")

	if !bytes.Equal(got, want) {
		t.Fatalf("normalizeTTYInput(%q) = %q, want %q", input, got, want)
	}

	if bytes.Equal(input, got) {
		t.Fatal("normalizeTTYInput should return a copy instead of mutating the input slice")
	}
}
