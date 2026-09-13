package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

func init() {
	cmd := &cobra.Command{Use: "recovery <service>", Short: "Inspect migrations requiring operator review", Args: cobra.ExactArgs(1)}
	jsonOutput := cmd.Flags().Bool("json", false, "print structured recovery status")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		var status api.RecoveryStatus
		if err := NewDaemonClient(SocketPath).Get(cmd.Context(), "/services/"+url.PathEscape(args[0])+"/recovery", &status); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Service: %s\nCurrent deployment: %s\n", status.Service, status.CurrentDeployID)
		if len(status.Deployments) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No marked unsuccessful migrations recorded.")
			return nil
		}
		for _, d := range status.Deployments {
			fmt.Fprintf(cmd.OutOrStdout(), "\nDeployment: %s (%s)\nReason: %s\nMigration task: %s (%s)\n", d.DeployID, d.Status, d.Reason, d.TaskName, d.TaskState)
			if d.ConfigError != "" {
				fmt.Fprintln(cmd.OutOrStdout(), d.ConfigError)
			}
			for _, v := range d.Volumes {
				fmt.Fprintf(cmd.OutOrStdout(), "Volume: %s\n", v.Name)
				for _, b := range v.Backups {
					fmt.Fprintf(cmd.OutOrStdout(), "  Backup candidate: %s (%s)\n", b.ID, b.CreatedAt)
				}
			}
		}
		for _, action := range status.NextActions {
			fmt.Fprintln(cmd.OutOrStdout(), "- "+action)
		}
		return nil
	}
	RootCmd.AddCommand(cmd)
}
