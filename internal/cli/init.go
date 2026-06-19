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
			homeDir = "/home/yumekaz"
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

		// Check if mini-docker is installed in standard path
		miniDockerPath := "/home/yumekaz/Desktop/Mini-Docker/mini_docker"
		if _, err := os.Stat(miniDockerPath); err == nil {
			fmt.Printf("Found Mini-Docker workspace at: %s\n", miniDockerPath)
		} else {
			fmt.Println("Warning: Mini-Docker codebase was not found in ~/Desktop/Mini-Docker. Make sure it is installed and running.")
		}

		fmt.Println("Initialization completed successfully. You can now start the daemon using 'cairn daemon start'.")
		return nil
	},
}

func init() {
	RootCmd.AddCommand(initCmd)
}
