package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [service_name]",
	Short: "Fetch logs for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		path := fmt.Sprintf("/services/%s/logs?follow=%t", serviceName, follow)
		if tail > 0 {
			path = fmt.Sprintf("%s&tail=%d", path, tail)
		}

		stream, err := client.Stream(ctx, path)
		if err != nil {
			return err
		}
		defer stream.Close()

		// Stream content directly to stdout
		_, err = io.Copy(os.Stdout, stream)
		if err != nil && err != io.EOF {
			return fmt.Errorf("error reading log stream: %w", err)
		}

		return nil
	},
}

func init() {
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	logsCmd.Flags().IntP("tail", "t", 0, "Number of lines to show from the end of the logs")
	RootCmd.AddCommand(logsCmd)
}
