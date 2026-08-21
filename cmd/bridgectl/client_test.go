package main

import "testing"

func TestDefaultServerNameFromTarget(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "host port",
			target: "d-5a0dd17c.tail6198c2.ts.net:9445",
			want:   "d-5a0dd17c.tail6198c2.ts.net",
		},
		{
			name:   "ipv4 host port",
			target: "127.0.0.1:9445",
			want:   "127.0.0.1",
		},
		{
			name:   "ipv6 host port",
			target: "[::1]:9445",
			want:   "::1",
		},
		{
			name:   "bare host",
			target: "bridge.example.com",
			want:   "bridge.example.com",
		},
		{
			name:   "unix target",
			target: "unix:///tmp/bridge.sock",
			want:   "server",
		},
		{
			name:   "ambiguous ipv6",
			target: "::1",
			want:   "server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultServerNameFromTarget(tt.target); got != tt.want {
				t.Fatalf("defaultServerNameFromTarget(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestNormalizeRemoteTarget(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "bare host",
			target: "d-dbaa4df8.tail6198c2.ts.net",
			want:   "d-dbaa4df8.tail6198c2.ts.net:9445",
		},
		{
			name:   "host port",
			target: "d-dbaa4df8.tail6198c2.ts.net:9445",
			want:   "d-dbaa4df8.tail6198c2.ts.net:9445",
		},
		{
			name:   "ipv4 host port",
			target: "127.0.0.1:9445",
			want:   "127.0.0.1:9445",
		},
		{
			name:   "ipv6 host port",
			target: "[::1]:9445",
			want:   "[::1]:9445",
		},
		{
			name:   "unix target",
			target: "unix:///tmp/bridge.sock",
			want:   "unix:///tmp/bridge.sock",
		},
		{
			name:   "ambiguous ipv6",
			target: "::1",
			want:   "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRemoteTarget(tt.target); got != tt.want {
				t.Fatalf("normalizeRemoteTarget(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}
