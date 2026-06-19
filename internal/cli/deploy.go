package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [config_path]",
	Short: "Deploy a service config (cairn.yaml) to the daemon",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := args[0]

		// 1. Parse service config
		svcConfig, err := config.ParseServiceConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to parse config %s: %w", configPath, err)
		}

		// 2. Submit to daemon
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
