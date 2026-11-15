package main

import (
	"context"
	"encoding/json"
	"flag"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8740", "TLS listen address")
	db := flag.String("db", "coordinator.db", "SQLite path")
	cert := flag.String("tls-cert", "", "TLS certificate PEM")
	key := flag.String("tls-key", "", "TLS key PEM")
	flag.Parse()
	if *cert == "" || *key == "" {
		log.Fatal("--tls-cert and --tls-key required")
	}
	var tokens map[string]string
	if e := json.Unmarshal([]byte(os.Getenv("DSH_WORKER_TOKENS")), &tokens); e != nil {
		log.Fatal("DSH_WORKER_TOKENS must be a JSON device-ID/token map")
	}
	s, e := coordinator.Open(coordinator.Config{DBPath: *db, ControllerToken: os.Getenv("DSH_CONTROLLER_TOKEN"), WorkerTokens: tokens})
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		s.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("Coordinator TLS listening on %s", *listen)
	if e = srv.ListenAndServeTLS(*cert, *key); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
