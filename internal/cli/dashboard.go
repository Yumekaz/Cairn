package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open the Cairn web dashboard in your browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		// 1. Verify daemon is running
		var daemonStatus api.DaemonStatus
		if err := client.Get(ctx, "/status", &daemonStatus); err != nil {
			return fmt.Errorf("cairnd daemon is not running (connect to socket failed). Start the daemon first using 'cairn daemon start'")
		}

		// 2. Load configuration to resolve the dashboard TCP address
		cfg, err := config.LoadDaemonConfig("")
		if err != nil {
			return fmt.Errorf("failed to load daemon configuration: %w", err)
		}

		if cfg.DashboardAddr == "" {
			return fmt.Errorf("dashboard address is disabled or not configured in ~/.cairn/cairnd-config.yaml")
		}

		url := fmt.Sprintf("http://%s", cfg.DashboardAddr)
		fmt.Printf("Cairn Dashboard is running at: %s\n", url)
		fmt.Println("Opening in default browser...")

		// 3. Trigger default web browser
		if err := openBrowser(url); err != nil {
			fmt.Printf("Warning: Failed to automatically open browser: %v. You can open the URL manually.\n", err)
		}

		return nil
	},
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", etc.
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

func init() {
	RootCmd.AddCommand(dashboardCmd)
}
