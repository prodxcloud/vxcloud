// Example: list pipelines using vxsdk-go.
//
// Run:
//
//	cd services/sdk/examples/basic
//	go run .
//
// Requires that `vxcli auth login` has been run first, OR set VXSDK_API_KEY
// in the environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	vxsdk "github.com/prodxcloud/vxcloud"
	vxerrors "github.com/prodxcloud/vxcloud/errors"
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

	var (
		c   *vxsdk.Client
		err error
	)
	if key := os.Getenv("VXSDK_API_KEY"); key != "" {
		c, err = vxsdk.New(ctx,
			vxsdk.WithAPIKey(key),
			vxsdk.WithUsername(os.Getenv("VXSDK_USERNAME")),
		)
	} else {
		c, err = vxsdk.LoadFromVxcli(ctx)
	}
	if err != nil {
		return err
	}

	fmt.Printf("authenticated as %q  (node=%s)\n", c.Whoami().Username, c.NodeURL())

	pipelines, err := c.CICD().Pipelines().List(ctx)
	if err != nil {
		var auth *vxerrors.AuthError
		if errors.As(err, &auth) {
			return fmt.Errorf("credentials rejected — re-run `vxcli auth login`: %w", err)
		}
		return err
	}

	fmt.Printf("\n%d pipeline(s):\n", len(pipelines))
	for _, p := range pipelines {
		fmt.Printf("  %-36s  %-28s  %s\n", p.ID, p.Name, p.RepositoryURL)
	}
	return nil
}
