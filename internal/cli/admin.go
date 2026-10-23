package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/config"
)

func init() {
	admin := &cobra.Command{Use: "admin", Short: "Prepare private TCP administration credentials"}
	var tokenFile string
	create := &cobra.Command{Use: "init", Short: "Create a private random admin token without displaying it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if tokenFile == "" {
			tokenFile = filepath.Join(config.DefaultConfig().DataDir, "admin.token")
		}
		if err := config.CreateAdminToken(tokenFile); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Admin credential created: %s\nUsername: cairn\nSet admin_token_file in the daemon configuration; restart to apply. Never publish the token.\n", tokenFile)
		return nil
	}}
	create.Flags().StringVar(&tokenFile, "token-file", "", "Private credential file (must not already exist)")
	admin.AddCommand(create)
	var configPath string
	export := &cobra.Command{Use: "config-json", Short: "Export resolved daemon settings (paths only, never key contents)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDaemonConfig(configPath)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
	}}
	export.Flags().StringVar(&configPath, "config", "", "Configuration file to resolve")
	admin.AddCommand(export)
	RootCmd.AddCommand(admin)
}
