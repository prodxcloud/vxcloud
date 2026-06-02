// Read-only live check for marketplace + nodes modules.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	vxsdk "github.com/prodxcloud/vxcloud"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := vxsdk.LoadFromVxcli(ctx)
	if err != nil {
		return err
	}

	agents, err := c.Marketplace().Agents().List(ctx)
	if err != nil {
		return fmt.Errorf("agents.List: %w", err)
	}
	fmt.Printf("agents:    %d\n", len(agents))
	for _, a := range agents {
		fmt.Printf("  %-32s  %-12s  %s\n", a.ID, a.Category, truncate(a.Description, 60))
	}

	models, err := c.Marketplace().Models().List(ctx)
	if err != nil {
		return fmt.Errorf("models.List: %w", err)
	}
	fmt.Printf("\nmodels:    %d\n", len(models))
	for _, m := range models {
		fmt.Printf("  %-32s  %-12s  %s\n", m.ID, m.Category, truncate(m.Description, 60))
	}

	solutions, err := c.Marketplace().Solutions().List(ctx)
	if err != nil {
		return fmt.Errorf("solutions.List: %w", err)
	}
	fmt.Printf("\nsolutions: %d\n", len(solutions))
	for _, s := range solutions {
		if len(s.Name) > 40 {
			s.Name = s.Name[:40]
		}
		fmt.Printf("  %-40s  %-10s  %-10s\n", s.Name, s.CloudProvider, s.Category)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
