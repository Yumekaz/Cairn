package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage volume backups",
}

var backupCreateCmd = &cobra.Command{
	Use:   "create [volume_name]",
	Short: "Create a manual backup snapshot of a volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		volumeName := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		fmt.Printf("Starting backup for volume '%s'...\n", volumeName)

		var backup api.Backup
		path := fmt.Sprintf("/volumes/%s/backups", volumeName)
		if err := client.Post(ctx, path, nil, &backup); err != nil {
			return err
		}

		fmt.Printf("Backup '%s' created successfully!\n", backup.ID)
		fmt.Printf("Status:    %s\n", backup.Status)
		fmt.Printf("Size:      %d bytes\n", backup.SizeBytes)
		fmt.Printf("Checksum:  %s\n", backup.Checksum)
		fmt.Printf("Path:      %s\n", backup.BackupPath)
		return nil
	},
}

var backupListCmd = &cobra.Command{
	Use:   "list [volume_name]",
	Short: "List all backups for a volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		volumeName := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var backups []*api.Backup
		path := fmt.Sprintf("/volumes/%s/backups", volumeName)
		if err := client.Get(ctx, path, &backups); err != nil {
			return err
		}

		if len(backups) == 0 {
			fmt.Printf("No backups found for volume '%s'.\n", volumeName)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "BACKUP ID\tSTATUS\tSIZE (BYTES)\tCHECKSUM\tCREATED AT")
		for _, b := range backups {
			shortChecksum := b.Checksum
			if len(shortChecksum) > 10 {
				shortChecksum = shortChecksum[:10]
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", b.ID, b.Status, b.SizeBytes, shortChecksum, b.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		w.Flush()

		return nil
	},
}

func init() {
	backupCmd.AddCommand(backupCreateCmd)
	backupCmd.AddCommand(backupListCmd)
	RootCmd.AddCommand(backupCmd)
}
