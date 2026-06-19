package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	SocketPath string
)

var RootCmd = &cobra.Command{
	Use:   "cairn",
	Short: "Cairn is a CLI-first, stateful-first self-hosted PaaS",
	Long:  `Cairn is a developer-friendly self-hosted PaaS optimized for running stateful applications.`,
}

func init() {
	RootCmd.PersistentFlags().StringVar(&SocketPath, "socket", GetDefaultSocketPath(), "unix socket path to connect to cairnd")
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
