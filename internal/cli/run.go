package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [service_name] \"[command]\"",
	Short: "Execute a one-off command in the service's environment",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		runCommand := args[1]

		client := NewDaemonClient(SocketPath)
		ctx := context.Background()

		body := map[string]string{
			"command": runCommand,
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/services/"+serviceName+"/run", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("cairnd connection failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			var errResp struct {
				Error string `json:"error"`
			}
			if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
				return fmt.Errorf("daemon error (status %d): %s", resp.StatusCode, errResp.Error)
			}
			return fmt.Errorf("daemon request failed with status: %d", resp.StatusCode)
		}

		reader := bufio.NewReader(resp.Body)
		exitCode := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if line != "" {
					if strings.HasPrefix(line, "[cairn-exit-code] ") {
						codeStr := strings.TrimSpace(strings.TrimPrefix(line, "[cairn-exit-code] "))
						if code, errCode := strconv.Atoi(codeStr); errCode == nil {
							exitCode = code
							break
						}
					}
					fmt.Print(line)
				}
				if err == io.EOF {
					break
				}
				return fmt.Errorf("error reading log stream: %w", err)
			}

			if strings.HasPrefix(line, "[cairn-exit-code] ") {
				codeStr := strings.TrimSpace(strings.TrimPrefix(line, "[cairn-exit-code] "))
				if code, errCode := strconv.Atoi(codeStr); errCode == nil {
					exitCode = code
					break
				}
			}

			fmt.Print(line)
		}

		if exitCode != 0 {
			os.Exit(exitCode)
		}

		return nil
	},
}

func init() {
	RootCmd.AddCommand(runCmd)
}
