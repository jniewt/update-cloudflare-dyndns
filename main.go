package main

import (
	"flag"
	log "log/slog"
	"net/http"
	"os"
)

func main() {

	bindAddr := flag.String("addr", ":8081", "address of the http server")

	restSrv, err := NewServer(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if err != nil {
		log.Error("Failed to create server", "error", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: restSrv, Addr: *bindAddr}
	if err = srv.ListenAndServe(); err != nil {
		log.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
