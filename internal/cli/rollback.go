package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback [service_name]",
	Short: "Roll back a service to a previous deployment version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		deployID, _ := cmd.Flags().GetString("to")
		force, _ := cmd.Flags().GetBool("force")

		if deployID == "" {
			return fmt.Errorf("required flag --to [deploy_id] is missing")
		}

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		req := map[string]interface{}{
			"deploy_id": deployID,
			"force":     force,
		}

		var result api.Service
		path := fmt.Sprintf("/services/%s/rollback", serviceName)
		err := client.Post(ctx, path, req, &result)
		if err != nil {
			// Check if it's a safety warning conflict (409)
			if strings.Contains(err.Error(), "status 409") {
				fmt.Println("⚠️  ROLLBACK SAFETY WARNING ⚠️")
				// Print error message cleanly
				cleanMsg := err.Error()
				if idx := strings.Index(cleanMsg, "): "); idx != -1 {
					cleanMsg = cleanMsg[idx+3:]
				}
				fmt.Println(cleanMsg)
				fmt.Println("\nTo force this rollback anyway, rerun the command with the --force flag.")
				return nil
			}
			return err
		}

		fmt.Printf("Service '%s' successfully rolled back to deployment '%s'!\n", serviceName, deployID)
		fmt.Printf("Current Deploy ID: %s\n", result.CurrentDeployID)
		fmt.Printf("Actual State:      %s\n", result.ActualState)
		return nil
	},
}

func init() {
	rollbackCmd.Flags().String("to", "", "Deploy ID to roll back to (required)")
	rollbackCmd.MarkFlagRequired("to")
	rollbackCmd.Flags().BoolP("force", "f", false, "Force rollback, bypassing safety warnings")
	RootCmd.AddCommand(rollbackCmd)
}
