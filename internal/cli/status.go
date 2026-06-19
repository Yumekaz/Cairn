package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of the Cairn daemon and services",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		// 1. Get daemon status
		var daemonStatus api.DaemonStatus
		if err := client.Get(ctx, "/status", &daemonStatus); err != nil {
			return err
		}

		fmt.Println("=== Cairn Daemon Status ===")
		fmt.Printf("Version:        %s\n", daemonStatus.Version)
		fmt.Printf("Uptime:         %s\n", daemonStatus.Uptime)
		fmt.Printf("Active Services:%d\n", daemonStatus.ActiveServices)
		fmt.Printf("Storage Usage:  %s\n", daemonStatus.StorageUsage)
		fmt.Println()

		// 2. Get registered services
		var services []*api.Service
		if err := client.Get(ctx, "/services", &services); err != nil {
			return err
		}

		fmt.Println("=== Services ===")
		if len(services) == 0 {
			fmt.Println("No services registered. Run 'cairn init' or deploy a service config.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tKIND\tDESIRED STATE\tACTUAL STATE\tROUTE")
		for _, s := range services {
			// truncate ID for readability
			shortID := s.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID, s.Name, s.Kind, s.DesiredState, s.ActualState, s.Route)
		}
		w.Flush()

		return nil
	},
}

func init() {
	RootCmd.AddCommand(statusCmd)
}
