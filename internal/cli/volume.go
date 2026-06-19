package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

var volumeCmd = &cobra.Command{
	Use:   "volume",
	Short: "Manage persistent volumes",
}

var volumeCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new named volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		mountPath, _ := cmd.Flags().GetString("mount")

		req := map[string]string{
			"name":       name,
			"mount_path": mountPath,
		}

		var vol api.Volume
		if err := client.Post(ctx, "/volumes", req, &vol); err != nil {
			return err
		}

		fmt.Printf("Volume '%s' created successfully!\n", vol.Name)
		fmt.Printf("Host Path: %s\n", vol.HostPath)
		return nil
	},
}

var volumeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all persistent volumes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var vols []*api.Volume
		if err := client.Get(ctx, "/volumes", &vols); err != nil {
			return err
		}

		if len(vols) == 0 {
			fmt.Println("No volumes found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tATTACHED SERVICE\tMOUNT PATH\tHOST PATH")
		for _, v := range vols {
			shortID := v.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			attachedSvc := v.AttachedServiceID
			if attachedSvc == "" {
				attachedSvc = "<none>"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID, v.Name, v.Status, attachedSvc, v.MountPath, v.HostPath)
		}
		w.Flush()

		return nil
	},
}

var volumeInspectCmd = &cobra.Command{
	Use:   "inspect [name]",
	Short: "Display detailed information on a volume",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var vol api.Volume
		path := fmt.Sprintf("/volumes/%s", name)
		if err := client.Get(ctx, path, &vol); err != nil {
			return err
		}

		fmt.Printf("ID:                 %s\n", vol.ID)
		fmt.Printf("Name:               %s\n", vol.Name)
		fmt.Printf("Status:             %s\n", vol.Status)
		fmt.Printf("Host Path:          %s\n", vol.HostPath)
		fmt.Printf("Attached Service:   %s\n", vol.AttachedServiceID)
		fmt.Printf("Mount Path:         %s\n", vol.MountPath)
		fmt.Printf("Created At:         %s\n", vol.CreatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

func init() {
	volumeCreateCmd.Flags().StringP("mount", "m", "/data", "default container mount path")
	volumeCmd.AddCommand(volumeCreateCmd)
	volumeCmd.AddCommand(volumeListCmd)
	volumeCmd.AddCommand(volumeInspectCmd)
	RootCmd.AddCommand(volumeCmd)
}
