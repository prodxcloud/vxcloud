// Live contract test: no-op script install via the SDK.
//
//	cd services/sdk/examples/install_script
//	go run .
//
// The script just echoes a line and exits 0. Creates one tenant session
// row; nothing on the target VM changes.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	vxsdk "github.com/prodxcloud/vxcloud"
	"github.com/prodxcloud/vxcloud/install"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c, err := vxsdk.LoadFromVxcli(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("authenticated as %q  (node=%s)\n", c.Whoami().Username, c.NodeURL())

	res, err := c.Install().Script(ctx, install.ScriptOpts{
		SSH: install.SSH{
			Host:        "54.197.71.181",
			User:        "ubuntu",
			KeyPairName: "AWSPRODKEY1.PEM",
		},
		ScriptName: "vxsdk-contract-test.sh",
		Script:     []byte("#!/bin/bash\nset -e\necho \"vxsdk-go contract test at $(date -u)\"\nexit 0\n"),
	})
	if err != nil {
		return err
	}

	fmt.Printf("\nresult:\n  session_id     %s\n  hostname       %s\n  exit_code      %d\n  execution_time %s\n  status         %s\n",
		res.SessionID, res.Hostname, res.ExitCode, res.ExecutionTime, res.Status)
	if res.Stdout != "" {
		fmt.Printf("  stdout         %q\n", res.Stdout)
	}
	return nil
}
