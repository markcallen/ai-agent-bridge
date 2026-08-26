package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewServerUsesCustomViteURL(t *testing.T) {
	t.Parallel()

	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("proxied path=%q want /", r.URL.Path)
		}
		_, _ = w.Write([]byte("vite dev server"))
	}))
	defer vite.Close()

	srv := newServer(5173, vite.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "vite dev server" {
		t.Fatalf("body=%q want vite dev server", got)
	}
}
