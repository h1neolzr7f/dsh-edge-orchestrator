package main

import (
	"context"
	"flag"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	id := flag.String("id", "", "paired device ID")
	endpoint := flag.String("coordinator", "https://localhost:8740", "Coordinator HTTPS URL")
	db := flag.String("db", "edge-agent.db", "persistent Worker SQLite path")
	ca := flag.String("ca", "", "additional trusted CA PEM")
	flag.Parse()
	client, e := edgelink.HTTPClient(*ca)
	if e != nil {
		log.Fatal(e)
	}
	a, e := agent.Open(agent.Config{DBPath: *db, DeviceID: *id, Token: os.Getenv("DSH_WORKER_TOKEN"), CoordinatorURL: *endpoint, HTTPClient: client})
	if e != nil {
		log.Fatal(e)
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Worker %s starting (device.inspect, report.aggregate)", *id)
	if e = a.Run(ctx); e != nil {
		log.Fatal(e)
	}
}
