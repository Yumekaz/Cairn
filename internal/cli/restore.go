package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore [volume_name_or_service_name] [backup_id]",
	Short: "Restore a volume or database service from a backup snapshot",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetName := args[0]
		backupID := args[1]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		fmt.Printf("Restoring '%s' from backup '%s'...\n", targetName, backupID)

		req := map[string]string{
			"backup_id": backupID,
		}

		path := fmt.Sprintf("/volumes/%s/restore", targetName)
		var resp struct {
			Status string `json:"status"`
		}
		if err := client.Post(ctx, path, req, &resp); err != nil {
			return err
		}

		fmt.Printf("Restored '%s' successfully from backup '%s'!\n", targetName, backupID)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(restoreCmd)
}
