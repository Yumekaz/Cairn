package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/preflight"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check local Cairn + Mini-Docker readiness",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDaemonConfig("")
		if err != nil {
			return err
		}

		fmt.Println("=== Cairn Doctor ===")
		fmt.Printf("Config data dir:     %s\n", cfg.DataDir)
		fmt.Printf("Configured MD sock:  %s\n", cfg.MiniDockerSocket)

		// Daemon socket
		if st, err := os.Stat(cfg.SocketPath); err == nil {
			fmt.Printf("cairnd socket:       %s (present)\n", cfg.SocketPath)
			_ = st
			// try status
			client := NewDaemonClient(SocketPath)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var status map[string]interface{}
			if err := client.Get(ctx, "/status", &status); err != nil {
				fmt.Printf("cairnd status:       unreachable (%v)\n", err)
			} else {
				fmt.Printf("cairnd status:       OK (version=%v)\n", status["version"])
			}
		} else {
			fmt.Printf("cairnd socket:       missing (%s) — run: cairn daemon start\n", cfg.SocketPath)
		}

		// Mini-Docker
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		md := preflight.ProbeMiniDocker(ctx, cfg.MiniDockerSocket)
		if md.OK {
			fmt.Printf("Mini-Docker:         %s\n", md.Message)
		} else {
			fmt.Printf("Mini-Docker:         FAIL — %s\n", md.Message)
			if md.Hint != "" {
				fmt.Printf("\nHint:\n%s\n", md.Hint)
			}
		}

		rootfs := preflight.DiscoverRootfs()
		if rootfs != "" {
			fmt.Printf("Rootfs discovery:    %s\n", rootfs)
		} else {
			fmt.Printf("Rootfs discovery:    not found (set CAIRN_ROOTFS or MINI_DOCKER_ROOTFS)\n")
		}

		if !md.OK {
			return fmt.Errorf("preflight failed: Mini-Docker is not usable")
		}
		fmt.Println("\nAll critical preflight checks passed.")
		return nil
	},
}

func init() {
	RootCmd.AddCommand(doctorCmd)
}
