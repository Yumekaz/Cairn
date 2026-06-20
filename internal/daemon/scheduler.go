package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// CronSchedule represents a parsed cron expression.
type CronSchedule struct {
	minutes map[int]bool
	hours   map[int]bool
	doms    map[int]bool
	months  map[int]bool
	dows    map[int]bool
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (*CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("invalid cron expression: expected 5 fields, got %d", len(fields))
	}

	minutes, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minutes: %w", err)
	}

	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hours: %w", err)
	}

	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("invalid day of month: %w", err)
	}

	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("invalid month: %w", err)
	}

	dows, err := parseCronField(fields[4], 0, 7) // 0-7, where 0 or 7 is Sunday
	if err != nil {
		return nil, fmt.Errorf("invalid day of week: %w", err)
	}

	if dows[7] {
		dows[0] = true
		delete(dows, 7)
	}

	return &CronSchedule{
		minutes: minutes,
		hours:   hours,
		doms:    doms,
		months:  months,
		dows:    dows,
	}, nil
}

func parseCronField(field string, min, max int) (map[int]bool, error) {
	vals := make(map[int]bool)

	if field == "*" {
		for i := min; i <= max; i++ {
			vals[i] = true
		}
		return vals, nil
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		if strings.Contains(part, "/") {
			stepParts := strings.Split(part, "/")
			if len(stepParts) != 2 {
				return nil, fmt.Errorf("invalid step: %s", part)
			}
			step, err := strconv.Atoi(stepParts[1])
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step value: %s", stepParts[1])
			}

			start := min
			end := max
			rangePart := stepParts[0]
			if rangePart != "*" {
				if strings.Contains(rangePart, "-") {
					rParts := strings.Split(rangePart, "-")
					if len(rParts) != 2 {
						return nil, fmt.Errorf("invalid range in step: %s", rangePart)
					}
					start, err = strconv.Atoi(rParts[0])
					if err != nil || start < min || start > max {
						return nil, fmt.Errorf("invalid start of range: %s", rParts[0])
					}
					end, err = strconv.Atoi(rParts[1])
					if err != nil || end < min || end > max || end < start {
						return nil, fmt.Errorf("invalid end of range: %s", rParts[1])
					}
				} else {
					start, err = strconv.Atoi(rangePart)
					if err != nil || start < min || start > max {
						return nil, fmt.Errorf("invalid range start: %s", rangePart)
					}
				}
			}

			for i := start; i <= end; i += step {
				vals[i] = true
			}
		} else if strings.Contains(part, "-") {
			rParts := strings.Split(part, "-")
			if len(rParts) != 2 {
				return nil, fmt.Errorf("invalid range: %s", part)
			}
			start, err := strconv.Atoi(rParts[0])
			if err != nil || start < min || start > max {
				return nil, fmt.Errorf("invalid start of range: %s", rParts[0])
			}
			end, err := strconv.Atoi(rParts[1])
			if err != nil || end < min || end > max || end < start {
				return nil, fmt.Errorf("invalid end of range: %s", rParts[1])
			}
			for i := start; i <= end; i++ {
				vals[i] = true
			}
		} else {
			val, err := strconv.Atoi(part)
			if err != nil || val < min || val > max {
				return nil, fmt.Errorf("invalid value: %s", part)
			}
			vals[val] = true
		}
	}

	return vals, nil
}

// Matches returns true if the schedule matches the given time.
func (s *CronSchedule) Matches(t time.Time) bool {
	minute := t.Minute()
	hour := t.Hour()
	dom := t.Day()
	month := int(t.Month())
	dow := int(t.Weekday())

	return s.minutes[minute] && s.hours[hour] && s.doms[dom] && s.months[month] && s.dows[dow]
}

// Scheduler handles background evaluation of cron schedules.
type Scheduler struct {
	store   *store.Store
	runtime runtime.RuntimeBackend
	dataDir string
}

// NewScheduler creates a new scheduler instance.
func NewScheduler(store *store.Store, r runtime.RuntimeBackend, dataDir string) *Scheduler {
	return &Scheduler{
		store:   store,
		runtime: r,
		dataDir: dataDir,
	}
}

// Start runs the background evaluation loop.
func (s *Scheduler) Start(ctx context.Context) {
	log.Println("Starting background cron scheduler loop...")
	lastTickMinute := -1
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping cron scheduler...")
			return
		case now := <-ticker.C:
			currentMinute := now.Minute()
			if currentMinute == lastTickMinute {
				continue
			}
			lastTickMinute = currentMinute

			// Execute tick in a separate goroutine so it doesn't block the minute check
			go s.runTick(ctx, now)
		}
	}
}

func (s *Scheduler) runTick(ctx context.Context, now time.Time) {
	cronJobs, err := s.store.ListCronJobs()
	if err != nil {
		log.Printf("Scheduler error listing cron jobs: %v\n", err)
		return
	}

	for _, cj := range cronJobs {
		schedule, err := ParseCron(cj.Schedule)
		if err != nil {
			log.Printf("Scheduler error parsing schedule for job %s (%s): %v\n", cj.Name, cj.Schedule, err)
			continue
		}

		if schedule.Matches(now) {
			log.Printf("Scheduler: Cron job %s matches current time. Spawning job execution...\n", cj.Name)
			go func(job *api.CronJob) {
				if err := s.ExecuteCronJob(ctx, job); err != nil {
					log.Printf("Scheduler error executing job %s: %v\n", job.Name, err)
				}
			}(cj)
		}
	}
}

// ExecuteCronJob runs a cron job container, streams the logs, and stores the status in the DB.
func (s *Scheduler) ExecuteCronJob(ctx context.Context, cj *api.CronJob) error {
	svc, err := s.store.GetService(cj.ServiceID)
	if err != nil {
		return fmt.Errorf("failed to get service %s: %w", cj.ServiceID, err)
	}
	if svc == nil {
		return fmt.Errorf("service not found for job: %s", cj.ServiceID)
	}

	if svc.CurrentDeployID == "" {
		return fmt.Errorf("service %s has no current deployment config to run from", svc.Name)
	}

	// Load configuration JSON from disk
	cfgPath := filepath.Join(s.dataDir, "services", svc.Name, fmt.Sprintf("deploy_%s.json", svc.CurrentDeployID))
	cfgJSON, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to read service deploy config at %s: %w", cfgPath, err)
	}

	var cfg api.ServiceConfig
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("failed to parse service deploy config: %w", err)
	}

	// Create job run history record
	runID := uuid.New().String()
	runName := fmt.Sprintf("%s-run-%s", cj.Name, runID[:8])
	jr := &api.JobRun{
		ID:        runID,
		ServiceID: svc.ID,
		CronJobID: &cj.ID,
		Type:      "cron",
		Name:      runName,
		Command:   cj.Command,
		Status:    "running",
		StartedAt: time.Now(),
		Logs:      "",
	}

	if err := s.store.CreateJobRun(jr); err != nil {
		return fmt.Errorf("failed to create job run record: %w", err)
	}

	// Construct task container config
	taskName := fmt.Sprintf("cairn-%s-cron-%s", svc.Name, runID[:8])
	taskCfg := &api.ServiceConfig{
		Name:        cfg.Name,
		Kind:        cfg.Kind,
		Image:       cfg.Image,
		Command:     []string{"/bin/sh", "-c", cj.Command},
		Environment: cfg.Environment,
		Volumes:     cfg.Volumes,
	}

	// Run container
	containerID, err := s.runtime.CreateContainer(ctx, taskCfg, taskName)
	if err != nil {
		s.failJobRun(jr, "Failed to create task container: "+err.Error())
		return err
	}

	defer func() {
		// Clean up container
		s.runtime.RemoveContainer(context.Background(), containerID)
	}()

	if err := s.runtime.StartContainer(ctx, containerID); err != nil {
		s.failJobRun(jr, "Failed to start task container: "+err.Error())
		return err
	}

	// Wait for execution to finish
	var exitCode int
	var runErr error
	for {
		info, err := s.runtime.InspectContainer(ctx, containerID)
		if err != nil {
			runErr = err
			break
		}
		if info.State == runtime.StateStopped || info.State == runtime.StateError {
			if info.ExitCode != nil {
				exitCode = *info.ExitCode
			} else {
				exitCode = -1
			}
			break
		}
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break
		case <-time.After(200 * time.Millisecond):
		}
	}

	if runErr != nil {
		s.failJobRun(jr, "Execution error: "+runErr.Error())
		return runErr
	}

	// Capture logs
	logs := s.getContainerLogs(ctx, containerID)
	jr.Logs = logs
	jr.ExitCode = &exitCode
	now := time.Now()
	jr.FinishedAt = &now
	if exitCode == 0 {
		jr.Status = "success"
	} else {
		jr.Status = "failed"
		jr.FailureReason = fmt.Sprintf("exit code %d", exitCode)
	}

	if err := s.store.UpdateJobRun(jr); err != nil {
		return fmt.Errorf("failed to update job run: %w", err)
	}

	return nil
}

func (s *Scheduler) getContainerLogs(ctx context.Context, id string) string {
	stream, err := s.runtime.StreamLogs(ctx, id, false, 500)
	if err != nil {
		return "failed to stream logs: " + err.Error()
	}
	defer stream.Close()
	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "failed to read logs: " + err.Error()
	}
	return string(bytes)
}

func (s *Scheduler) failJobRun(jr *api.JobRun, reason string) {
	jr.Status = "failed"
	jr.FailureReason = reason
	now := time.Now()
	jr.FinishedAt = &now
	s.store.UpdateJobRun(jr)
}
