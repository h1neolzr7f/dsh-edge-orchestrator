package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type workspaceFlags map[string]string

func (w workspaceFlags) String() string { b, _ := json.Marshal(w); return string(b) }
func (w workspaceFlags) Set(s string) error {
	name, path, ok := strings.Cut(s, "=")
	if !ok || name == "" || path == "" {
		return fmt.Errorf("workspace must be logical_name=absolute_host_path")
	}
	w[name] = path
	return nil
}
func main() {
	pair := flag.String("pair", "", "single-use Coordinator pairing code")
	id := flag.String("id", "", "paired device ID")
	endpoint := flag.String("coordinator", "https://localhost:8740", "Coordinator HTTPS URL")
	db := flag.String("db", "edge-agent.db", "persistent Worker SQLite path")
	ca := flag.String("ca", "", "additional trusted CA PEM")
	roots := workspaceFlags{}
	if raw := os.Getenv("DSH_WORKSPACES"); raw != "" {
		if e := json.Unmarshal([]byte(raw), &roots); e != nil {
			log.Fatal("invalid DSH_WORKSPACES JSON")
		}
	}
	flag.Var(roots, "workspace", "logical_name=absolute_host_path (repeatable)")
	programs := flag.String("allow-programs", os.Getenv("DSH_ALLOWED_PROGRAMS"), "comma-separated or JSON array of allowed process programs")
	grace := flag.Duration("termination-grace", 500*time.Millisecond, "process termination grace")
	flag.Parse()
	var allowed []string
	explicit := false
	flag.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "allow-programs" })
	if *programs != "" || explicit {
		if strings.HasPrefix(strings.TrimSpace(*programs), "[") {
			if e := json.Unmarshal([]byte(*programs), &allowed); e != nil {
				log.Fatal("invalid allowed-program JSON")
			}
		} else {
			allowed = []string{}
			for _, p := range strings.Split(*programs, ",") {
				if p = strings.TrimSpace(p); p != "" {
					allowed = append(allowed, p)
				}
			}
		}
	}
	if *grace < 0 {
		log.Fatal("negative termination grace")
	}
	client, e := edgelink.HTTPClient(*ca)
	if e != nil {
		log.Fatal(e)
	}
	a, e := agent.Open(agent.Config{PairingCode: *pair, DBPath: *db, DeviceID: *id, Token: os.Getenv("DSH_WORKER_TOKEN"), CoordinatorURL: *endpoint, HTTPClient: client, Workspaces: roots, AllowedPrograms: allowed, TerminationGrace: *grace})
	if e != nil {
		log.Fatal(e)
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Worker %s starting with authorized workspaces and local capabilities", *id)
	if e = a.Run(ctx); e != nil {
		log.Fatal(e)
	}
}
