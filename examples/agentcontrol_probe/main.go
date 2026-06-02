// Phase 1 / Go SDK parity test for AgentControl APIs.
// Run from this directory: `go run .`
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	vxsdk "github.com/prodxcloud/vxcloud"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := vxsdk.LoadFromVxcli(ctx, vxsdk.WithTenantID("joelwembo"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	fmt.Printf("authenticated as %q  (node=%s, tenant=%s)\n", c.Whoami().Username, c.NodeURL(), c.TenantID())

	ac := c.AgentControl()
	probes := []struct {
		label string
		fn    func() (int, error)
	}{
		{"agents", func() (int, error) { l, err := ac.Agents().List(ctx); return len(l), err }},
		{"datasets", func() (int, error) { l, err := ac.Datasets().List(ctx); return len(l), err }},
		{"fine_tuning_jobs", func() (int, error) { l, err := ac.FineTuning().List(ctx); return len(l), err }},
		{"knowledge_bases", func() (int, error) { l, err := ac.Knowledge().List(ctx); return len(l), err }},
	}
	for _, p := range probes {
		n, err := p.fn()
		if err != nil {
			fmt.Printf("  %s: ERR %v\n", p.label, err)
		} else {
			fmt.Printf("  %s: %d\n", p.label, n)
		}
	}
}
