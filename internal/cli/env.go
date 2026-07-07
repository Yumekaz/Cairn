package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

// Env command definitions
var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage service environment variables",
}

var envSetCmd = &cobra.Command{
	Use:   "set [service_name] [KEY=VALUE]",
	Short: "Set a custom environment variable",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		kv := args[1]
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid KEY=VALUE format: %s", kv)
		}
		key, value := parts[0], parts[1]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		req := map[string]interface{}{
			"key":       key,
			"value":     value,
			"is_secret": false,
		}

		path := fmt.Sprintf("/services/%s/env", serviceName)
		var res map[string]string
		fmt.Printf("Updating environment variable and redeploying service '%s'...\n", serviceName)
		if err := client.Post(ctx, path, req, &res); err != nil {
			return err
		}

		fmt.Printf("Environment variable '%s' set and service redeployed successfully!\n", key)
		return nil
	},
}

var envListCmd = &cobra.Command{
	Use:   "list [service_name]",
	Short: "List custom environment variables",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var envs []*api.ServiceEnvVar
		path := fmt.Sprintf("/services/%s/env", serviceName)
		if err := client.Get(ctx, path, &envs); err != nil {
			return err
		}

		// Filter to standard env vars (not secrets)
		var filtered []*api.ServiceEnvVar
		for _, env := range envs {
			if !env.IsSecret {
				filtered = append(filtered, env)
			}
		}

		if len(filtered) == 0 {
			fmt.Println("No custom environment variables found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		for _, env := range filtered {
			fmt.Fprintf(w, "%s\t%s\n", env.Key, env.Value)
		}
		w.Flush()
		return nil
	},
}

var envRemoveCmd = &cobra.Command{
	Use:   "remove [service_name] [KEY]",
	Short: "Remove a custom environment variable",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		key := args[1]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		path := fmt.Sprintf("/services/%s/env/%s", serviceName, key)
		var res map[string]string
		fmt.Printf("Removing environment variable and redeploying service '%s'...\n", serviceName)
		if err := client.Delete(ctx, path, &res); err != nil {
			return err
		}

		fmt.Printf("Environment variable '%s' removed and service redeployed successfully!\n", key)
		return nil
	},
}

// Secret command definitions
var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage service secrets",
}

var secretSetCmd = &cobra.Command{
	Use:   "set [service_name] [KEY=VALUE]",
	Short: "Set a service secret",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		kv := args[1]
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid KEY=VALUE format: %s", kv)
		}
		key, value := parts[0], parts[1]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		req := map[string]interface{}{
			"key":       key,
			"value":     value,
			"is_secret": true,
		}

		path := fmt.Sprintf("/services/%s/env", serviceName)
		var res map[string]string
		fmt.Printf("Updating secret and redeploying service '%s'...\n", serviceName)
		if err := client.Post(ctx, path, req, &res); err != nil {
			return err
		}

		fmt.Printf("Secret '%s' set and service redeployed successfully!\n", key)
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:   "list [service_name]",
	Short: "List service secrets",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var envs []*api.ServiceEnvVar
		path := fmt.Sprintf("/services/%s/env", serviceName)
		if err := client.Get(ctx, path, &envs); err != nil {
			return err
		}

		// Filter to secrets only
		var filtered []*api.ServiceEnvVar
		for _, env := range envs {
			if env.IsSecret {
				filtered = append(filtered, env)
			}
		}

		if len(filtered) == 0 {
			fmt.Println("No secrets found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		for _, env := range filtered {
			fmt.Fprintf(w, "%s\t%s\n", env.Key, env.Value)
		}
		w.Flush()
		return nil
	},
}

var secretRemoveCmd = &cobra.Command{
	Use:   "remove [service_name] [KEY]",
	Short: "Remove a service secret",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		key := args[1]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		path := fmt.Sprintf("/services/%s/env/%s", serviceName, key)
		var res map[string]string
		fmt.Printf("Removing secret and redeploying service '%s'...\n", serviceName)
		if err := client.Delete(ctx, path, &res); err != nil {
			return err
		}

		fmt.Printf("Secret '%s' removed and service redeployed successfully!\n", key)
		return nil
	},
}

func init() {
	// Register env commands
	envCmd.AddCommand(envSetCmd)
	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envRemoveCmd)
	RootCmd.AddCommand(envCmd)

	// Register secret commands
	secretCmd.AddCommand(secretSetCmd)
	secretCmd.AddCommand(secretListCmd)
	secretCmd.AddCommand(secretRemoveCmd)
	RootCmd.AddCommand(secretCmd)
}
