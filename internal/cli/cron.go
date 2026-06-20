package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/yumekaz/cairn/internal/api"
)

type CreateCronJobRequest struct {
	ServiceName string `json:"service_name"`
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
}

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage and view scheduled cron jobs",
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all scheduled cron jobs",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var jobs []*api.CronJob
		if err := client.Get(ctx, "/cron", &jobs); err != nil {
			return err
		}

		if len(jobs) == 0 {
			fmt.Println("No cron jobs registered.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "JOB ID\tSERVICE ID\tJOB NAME\tSCHEDULE\tCOMMAND")
		for _, j := range jobs {
			shortID := j.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			shortSvcID := j.ServiceID
			if len(shortSvcID) > 8 {
				shortSvcID = shortSvcID[:8]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", shortID, shortSvcID, j.Name, j.Schedule, j.Command)
		}
		w.Flush()
		return nil
	},
}

var cronAddCmd = &cobra.Command{
	Use:   "add [service_name] [job_name] [schedule] [command]",
	Short: "Register a new cron job",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		jobName := args[1]
		schedule := args[2]
		command := args[3]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		reqBody := CreateCronJobRequest{
			ServiceName: serviceName,
			Name:        jobName,
			Schedule:    schedule,
			Command:     command,
		}

		var cj api.CronJob
		if err := client.Post(ctx, "/cron/add", reqBody, &cj); err != nil {
			return err
		}

		fmt.Printf("Cron job '%s' successfully registered for service '%s'!\n", cj.Name, serviceName)
		return nil
	},
}

var cronRemoveCmd = &cobra.Command{
	Use:   "remove [job_name]",
	Short: "Remove a scheduled cron job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobName := args[0]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		path := fmt.Sprintf("/cron/%s", jobName)
		if err := client.Delete(ctx, path, nil); err != nil {
			return err
		}

		fmt.Printf("Cron job '%s' successfully removed.\n", jobName)
		return nil
	},
}

var cronHistoryCmd = &cobra.Command{
	Use:   "history [job_name]",
	Short: "View run history of a scheduled cron job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jobName := args[0]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		var history []*api.JobRun
		path := fmt.Sprintf("/cron/%s/history", jobName)
		if err := client.Get(ctx, path, &history); err != nil {
			return err
		}

		if len(history) == 0 {
			fmt.Printf("No run history found for cron job '%s'.\n", jobName)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "RUN ID\tSTATUS\tSTARTED AT\tFINISHED AT\tEXIT CODE\tFAILURE REASON")
		for _, jr := range history {
			shortID := jr.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			exitCodeStr := "-"
			if jr.ExitCode != nil {
				exitCodeStr = strconv.Itoa(*jr.ExitCode)
			}
			finishedAtStr := "-"
			if jr.FinishedAt != nil {
				finishedAtStr = jr.FinishedAt.Format("2006-01-02 15:04:05")
			}
			failureReason := jr.FailureReason
			if failureReason == "" {
				failureReason = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				shortID,
				jr.Status,
				jr.StartedAt.Format("2006-01-02 15:04:05"),
				finishedAtStr,
				exitCodeStr,
				failureReason,
			)
		}
		w.Flush()
		return nil
	},
}

func init() {
	cronCmd.AddCommand(cronListCmd)
	cronCmd.AddCommand(cronAddCmd)
	cronCmd.AddCommand(cronRemoveCmd)
	cronCmd.AddCommand(cronHistoryCmd)
	RootCmd.AddCommand(cronCmd)
}
