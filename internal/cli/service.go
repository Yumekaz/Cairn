package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List all deployed services",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var services []*api.Service
		if err := client.Get(ctx, "/services", &services); err != nil {
			return err
		}

		if len(services) == 0 {
			fmt.Println("No services found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "SERVICE ID\tNAME\tRUNTIME ID\tDESIRED STATE\tACTUAL STATE\tROUTE")
		for _, s := range services {
			shortID := s.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			shortRuntimeID := s.RuntimeID
			if len(shortRuntimeID) > 12 {
				shortRuntimeID = shortRuntimeID[:12]
			} else if shortRuntimeID == "" {
				shortRuntimeID = "<none>"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID, s.Name, shortRuntimeID, s.DesiredState, s.ActualState, s.Route)
		}
		w.Flush()
		return nil
	},
}

var inspectCmd = &cobra.Command{
	Use:   "inspect [service_name]",
	Short: "Display detailed information on a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var s api.Service
		path := fmt.Sprintf("/services/%s", name)
		if err := client.Get(ctx, path, &s); err != nil {
			return err
		}

		fmt.Printf("ID:                %s\n", s.ID)
		fmt.Printf("Name:              %s\n", s.Name)
		fmt.Printf("Kind:              %s\n", s.Kind)
		fmt.Printf("Runtime Backend:   %s\n", s.RuntimeBackend)
		fmt.Printf("Runtime ID:        %s\n", s.RuntimeID)
		fmt.Printf("Current Deploy ID: %s\n", s.CurrentDeployID)
		fmt.Printf("Desired State:     %s\n", s.DesiredState)
		fmt.Printf("Actual State:      %s\n", s.ActualState)
		fmt.Printf("Route:             %s\n", s.Route)
		fmt.Printf("Created At:        %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Updated At:        %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

var startCmd = &cobra.Command{
	Use:   "start [service_name]",
	Short: "Start a stopped service container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var s api.Service
		path := fmt.Sprintf("/services/%s/start", name)
		if err := client.Post(ctx, path, nil, &s); err != nil {
			return err
		}

		fmt.Printf("Service '%s' started successfully!\n", name)
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [service_name]",
	Short: "Stop a running service container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var s api.Service
		path := fmt.Sprintf("/services/%s/stop", name)
		if err := client.Post(ctx, path, nil, &s); err != nil {
			return err
		}

		fmt.Printf("Service '%s' stopped successfully!\n", name)
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart [service_name]",
	Short: "Restart a running service container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var s api.Service
		path := fmt.Sprintf("/services/%s/restart", name)
		if err := client.Post(ctx, path, nil, &s); err != nil {
			return err
		}

		fmt.Printf("Service '%s' restarted successfully!\n", name)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(psCmd)
	RootCmd.AddCommand(inspectCmd)
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(stopCmd)
	RootCmd.AddCommand(restartCmd)
}
