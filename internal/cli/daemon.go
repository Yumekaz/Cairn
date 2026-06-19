package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Cairn daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Cairn daemon in the background",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Find cairnd binary path - look in current folder first, then PATH
		cairndPath, err := exec.LookPath("cairnd")
		if err != nil {
			// Try looking in ./bin/cairnd or ./cairnd
			if _, errOpt := os.Stat("./bin/cairnd"); errOpt == nil {
				cairndPath = "./bin/cairnd"
			} else if _, errOpt := os.Stat("./cairnd"); errOpt == nil {
				cairndPath = "./cairnd"
			} else {
				return fmt.Errorf("cairnd executable not found in PATH or current directory. Have you run 'make build'?")
			}
		}

		// Try starting it as a background process
		proc := exec.Command(cairndPath)
		// Detach process
		proc.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}

		if err := proc.Start(); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}

		fmt.Printf("Cairn daemon (cairnd) started in the background (PID %d)\n", proc.Process.Pid)
		fmt.Println("Wait for socket to be initialized...")

		// Wait up to 5 seconds for socket file to appear
		socketPath := SocketPath
		for i := 0; i < 20; i++ {
			if _, err := os.Stat(socketPath); err == nil {
				fmt.Println("Daemon is ready!")
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}

		return fmt.Errorf("daemon started but socket at %s did not become ready", socketPath)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running Cairn daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "/home/yumekaz"
		}
		pidFile := filepath.Join(homeDir, ".cairn", "cairnd.pid")

		data, err := os.ReadFile(pidFile)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no daemon PID file found at %s. Is the daemon running?", pidFile)
			}
			return err
		}

		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return fmt.Errorf("invalid PID in pidfile: %w", err)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find daemon process: %w", err)
		}

		fmt.Printf("Sending SIGTERM to cairnd (PID %d)...\n", pid)
		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to send SIGTERM: %w", err)
		}

		// Wait for process to clean up and delete PID file
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(pidFile); os.IsNotExist(err) {
				fmt.Println("Cairn daemon stopped successfully.")
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}

		return fmt.Errorf("daemon did not stop within timeout")
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	RootCmd.AddCommand(daemonCmd)
}
