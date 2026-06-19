package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Show the event timeline for the Cairn daemon and services",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var events []*api.Event
		if err := client.Get(ctx, "/events", &events); err != nil {
			return err
		}

		if len(events) == 0 {
			fmt.Println("No events recorded yet.")
			return nil
		}

		fmt.Println("=== Event Timeline ===")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "TIMESTAMP\tTYPE\tMESSAGE")
		for _, e := range events {
			formattedTime := e.CreatedAt.Format("2006-01-02 15:04:05")
			fmt.Fprintf(w, "%s\t%s\t%s\n", formattedTime, e.Type, e.Message)
		}
		w.Flush()

		return nil
	},
}

func init() {
	RootCmd.AddCommand(eventsCmd)
}
