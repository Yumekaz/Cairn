package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/preflight"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [config_path]",
	Short: "Deploy a service config (file or directory containing cairn.yaml)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := args[0]

		// Ensure CAIRN_ROOTFS is set when examples use ${CAIRN_ROOTFS}
		if os.Getenv("CAIRN_ROOTFS") == "" && os.Getenv("MINI_DOCKER_ROOTFS") == "" {
			if root := preflight.DiscoverRootfs(); root != "" {
				_ = os.Setenv("CAIRN_ROOTFS", root)
			}
		}

		// 1. Parse service config (file or directory)
		svcConfig, err := config.ParseServiceConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to parse config %s: %w", configPath, err)
		}
		if svcConfig.Image == "" || svcConfig.Image == "${CAIRN_ROOTFS}" {
			return fmt.Errorf("image/rootfs unresolved; set CAIRN_ROOTFS or MINI_DOCKER_ROOTFS to a Mini-Docker rootfs path")
		}

		// 2. Preflight Mini-Docker so deploy does not fail with opaque EOF
		cfg, _ := config.LoadDaemonConfig("")
		sock := ""
		if cfg != nil {
			sock = cfg.MiniDockerSocket
		}
		ctxProbe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		md := preflight.ProbeMiniDocker(ctxProbe, sock)
		if !md.OK {
			return fmt.Errorf("Mini-Docker preflight failed: %s\n%s", md.Message, md.Hint)
		}

		// 3. Submit to daemon
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var result api.Service
		err = client.Post(ctx, "/services", svcConfig, &result)
		if err != nil {
			return err
		}

		fmt.Printf("Service '%s' (ID: %s) registered and deployed successfully!\n", result.Name, result.ID)
		fmt.Printf("Current Deploy ID: %s\n", result.CurrentDeployID)
		fmt.Printf("Actual State:      %s\n", result.ActualState)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(deployCmd)
}
