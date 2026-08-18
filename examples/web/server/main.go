package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	vitePort := flag.Int("vite-port", 5173, "Vite dev server port (0 = production mode)")
	flag.Parse()

	srv := newServer(*vitePort)
	addr := fmt.Sprintf(":%d", *port)
	slog.Info("starting web server", "addr", addr, "dev", *vitePort > 0)
	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}
