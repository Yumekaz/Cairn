package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the Cairn system paths and configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			// Never hardcode a username path; last-resort fallback for portability.
			homeDir = os.TempDir()
		}

		cairnDir := filepath.Join(homeDir, ".cairn")
		fmt.Printf("Initializing Cairn home directory at %s...\n", cairnDir)
		if err := os.MkdirAll(cairnDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		configPath := filepath.Join(cairnDir, "cairnd-config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			fmt.Printf("Configuration file already exists at %s, skipping creation.\n", configPath)
		} else if os.IsNotExist(err) {
			fmt.Printf("Creating default configuration file at %s...\n", configPath)
			defaultCfg := config.DefaultConfig()

			// Save config
			if err := config.SaveDaemonConfig(defaultCfg, configPath); err != nil {
				return fmt.Errorf("failed to write config: %w", err)
			}
			fmt.Printf("Default config written successfully!\n")
			fmt.Printf("- Database Path: %s\n", defaultCfg.DatabasePath)
			fmt.Printf("- Volume Path:   %s\n", defaultCfg.VolumeDir)
			fmt.Printf("- Backup Path:   %s\n", defaultCfg.BackupDir)
		} else {
			return err
		}

		// Discover Mini-Docker rootfs without hardcoding a username path
		if root := os.Getenv("CAIRN_ROOTFS"); root != "" {
			fmt.Printf("CAIRN_ROOTFS: %s\n", root)
		} else if root := os.Getenv("MINI_DOCKER_ROOTFS"); root != "" {
			fmt.Printf("MINI_DOCKER_ROOTFS: %s\n", root)
		} else {
			fmt.Println("Tip: export CAIRN_ROOTFS=/path/to/Mini-Docker/rootfs for portable examples.")
			fmt.Println("     Then run: cairn doctor && scripts/clean_demo.sh")
		}

		fmt.Println("Initialization completed successfully. You can now start the daemon using 'cairn daemon start'.")
		return nil
	},
}

func init() {
	RootCmd.AddCommand(initCmd)
}
