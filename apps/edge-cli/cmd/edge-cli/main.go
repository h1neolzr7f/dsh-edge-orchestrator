package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"strconv"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	endpoint := flag.String("url", "https://localhost:8740", "Coordinator URL")
	ca := flag.String("ca", "", "trusted CA PEM")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return fmt.Errorf("commands: approvals | approve/deny ID [REASON] | pair [DEVICE_ID] | devices | snapshot | submit FILE | plan ID | watch ID | cancel/pause/resume ID | events [AFTER [THROUGH]]")
	}
	httpClient, e := edgelink.HTTPClient(*ca)
	if e != nil {
		return e
	}
	c := edgelink.NewClient(*endpoint, os.Getenv("DSH_CONTROLLER_TOKEN"), httpClient)
	ctx := context.Background()
	var out any
	switch args[0] {
	case "approvals":
		out, e = c.Approvals(ctx)
	case "approve", "deny":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("%s ID [REASON]", args[0])
		}
		decision := "approved"
		if args[0] == "deny" {
			decision = "denied"
		}
		reason := ""
		if len(args) == 3 {
			reason = args[2]
		}
		out, e = c.ResolveApproval(ctx, args[1], protocol.ApprovalDecision{Decision: decision, Reason: reason})
	case "pair", "pair-recover":
		if len(args) > 2 {
			return fmt.Errorf("pair [DEVICE_ID]")
		}
		hint := ""
		if len(args) == 2 {
			hint = args[1]
		}
		if args[0] == "pair-recover" && hint == "" {
			return fmt.Errorf("pair-recover DEVICE_ID")
		}
		out, e = c.CreatePairing(ctx, protocol.PairingRequest{DeviceHint: hint, CoordinatorURL: *endpoint, RecoverExisting: args[0] == "pair-recover"})
	case "devices":
		out, e = c.Devices(ctx)
	case "snapshot":
		out, e = c.Snapshot(ctx)
	case "submit":
		if len(args) != 2 {
			return fmt.Errorf("submit FILE")
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var p protocol.PlanEnvelope
		if e = protocol.Decode(b, &p); e != nil {
			return e
		}
		out, e = c.Submit(ctx, p)
	case "plan", "cancel", "pause", "resume":
		if len(args) != 2 {
			return fmt.Errorf("%s ID", args[0])
		}
		if args[0] == "plan" {
			out, e = c.Plan(ctx, args[1])
		} else {
			out, e = c.Control(ctx, args[1], args[0])
		}
	case "events":
		var cursor, through int64
		if len(args) > 1 {
			cursor, e = strconv.ParseInt(args[1], 10, 64)
			if e != nil {
				return e
			}
		}
		if len(args) > 2 {
			through, e = strconv.ParseInt(args[2], 10, 64)
			if e != nil {
				return e
			}
		}
		out, e = c.Events(ctx, cursor, through, 500)
	case "watch":
		if len(args) != 2 {
			return fmt.Errorf("watch ID")
		}
		last := ""
		for {
			p, err := c.Plan(ctx, args[1])
			if err != nil {
				return err
			}
			b, err := json.Marshal(p)
			if err != nil {
				return err
			}
			if string(b) != last {
				fmt.Println(string(b))
				last = string(b)
			}
			if protocol.Terminal(p.Status) {
				if p.Status != "succeeded" {
					return fmt.Errorf("plan ended: %s", p.Status)
				}
				return nil
			}
			time.Sleep(300 * time.Millisecond)
		}
	default:
		return fmt.Errorf("unknown command %s", args[0])
	}
	if e != nil {
		return e
	}
	b, e := json.MarshalIndent(out, "", "  ")
	if e != nil {
		return e
	}
	fmt.Println(string(b))
	return nil
}
