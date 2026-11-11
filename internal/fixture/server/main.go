// Command server runs the fixture gateway for local experiments:
//
//	go run ./internal/fixture/server -addr 127.0.0.1:8787
//
// See docs/development.md for the walkthrough.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/steermi/cli/internal/fixture"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	flag.Parse()
	log.Printf("fixture Steermi gateway on http://%s (pairing code %s)", *addr, fixture.PairingCode)
	srv := &http.Server{Addr: *addr, Handler: logRequests(fixture.New()), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}
