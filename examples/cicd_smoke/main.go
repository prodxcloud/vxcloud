// Smoke test: drive cicd Pipeline.Trigger + Build.Show via the Go SDK
// to confirm the CLI-observed failure is server-side, not CLI-side.
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := vxsdk.LoadFromVxcli(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("authed as %q  node=%s\n\n", c.Whoami().Username, c.NodeURL())

	pipelineID := "aa5d52fb-ae8e-4dd1-9455-9acdbf0dac81" // vxcloud-quickstart-fastapi

	p, err := c.CICD().Pipelines().Show(ctx, pipelineID)
	if err != nil {
		return fmt.Errorf("pipelines.Show: %w", err)
	}
	fmt.Printf("pipeline.Show -> %s (%s, %s)\n", p.Name, p.Status, p.RepositoryURL)

	b, err := c.CICD().Pipelines().Trigger(ctx, pipelineID, "main")
	if err != nil {
		return fmt.Errorf("pipelines.Trigger: %w", err)
	}
	fmt.Printf("pipelines.Trigger -> build_id=%s status=%s\n", b.ID, b.Status)

	time.Sleep(8 * time.Second)

	shown, err := c.CICD().Builds().Show(ctx, b.ID)
	if err != nil {
		return fmt.Errorf("builds.Show: %w", err)
	}
	fmt.Printf("builds.Show  -> status=%s started=%s completed=%s\n", shown.Status, shown.StartedAt.Format(time.RFC3339), shown.CompletedAt.Format(time.RFC3339))
	if shown.Error != "" {
		fmt.Printf("              error: %s\n", shown.Error)
	}
	if shown.LogsURL != "" {
		fmt.Printf("              logs_url: %s\n", shown.LogsURL)
	}
	return nil
}