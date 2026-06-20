package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// CreateJobRun creates a new job run record.
func (s *Store) CreateJobRun(jr *api.JobRun) error {
	if jr.StartedAt.IsZero() {
		jr.StartedAt = time.Now()
	}

	var exitCode interface{}
	if jr.ExitCode != nil {
		exitCode = *jr.ExitCode
	}

	var finishedAt interface{}
	if jr.FinishedAt != nil {
		finishedAt = *jr.FinishedAt
	}

	_, err := s.db.Exec(`
		INSERT INTO jobs (id, service_id, cron_job_id, type, name, command, status, exit_code, started_at, finished_at, logs, failure_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jr.ID, jr.ServiceID, toNullString(jr.CronJobID), jr.Type, jr.Name, jr.Command, jr.Status, exitCode, jr.StartedAt, finishedAt, jr.Logs, jr.FailureReason)
	return err
}

// UpdateJobRun updates an existing job run.
func (s *Store) UpdateJobRun(jr *api.JobRun) error {
	var exitCode interface{}
	if jr.ExitCode != nil {
		exitCode = *jr.ExitCode
	}

	var finishedAt interface{}
	if jr.FinishedAt != nil {
		finishedAt = *jr.FinishedAt
	}

	_, err := s.db.Exec(`
		UPDATE jobs SET
			status = ?,
			exit_code = ?,
			finished_at = ?,
			logs = ?,
			failure_reason = ?
		WHERE id = ?`,
		jr.Status, exitCode, finishedAt, jr.Logs, jr.FailureReason, jr.ID)
	return err
}

// GetJobRun retrieves a job run by ID.
func (s *Store) GetJobRun(id string) (*api.JobRun, error) {
	row := s.db.QueryRow(`
		SELECT id, service_id, cron_job_id, type, name, command, status, exit_code, started_at, finished_at, logs, failure_reason
		FROM jobs WHERE id = ?`, id)

	var jr api.JobRun
	var cronJobID sql.NullString
	var exitCode sql.NullInt64
	var finishedAt sql.NullTime
	var failureReason sql.NullString

	err := row.Scan(&jr.ID, &jr.ServiceID, &cronJobID, &jr.Type, &jr.Name, &jr.Command, &jr.Status, &exitCode, &jr.StartedAt, &finishedAt, &jr.Logs, &failureReason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	jr.CronJobID = scanStringPtr(cronJobID)
	if exitCode.Valid {
		val := int(exitCode.Int64)
		jr.ExitCode = &val
	}
	if finishedAt.Valid {
		jr.FinishedAt = &finishedAt.Time
	}
	if failureReason.Valid {
		jr.FailureReason = failureReason.String
	}

	return &jr, nil
}

// ListJobRuns retrieves all job runs.
func (s *Store) ListJobRuns() ([]*api.JobRun, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, cron_job_id, type, name, command, status, exit_code, started_at, finished_at, logs, failure_reason
		FROM jobs ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*api.JobRun
	for rows.Next() {
		var jr api.JobRun
		var cronJobID sql.NullString
		var exitCode sql.NullInt64
		var finishedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&jr.ID, &jr.ServiceID, &cronJobID, &jr.Type, &jr.Name, &jr.Command, &jr.Status, &exitCode, &jr.StartedAt, &finishedAt, &jr.Logs, &failureReason)
		if err != nil {
			return nil, err
		}

		jr.CronJobID = scanStringPtr(cronJobID)
		if exitCode.Valid {
			val := int(exitCode.Int64)
			jr.ExitCode = &val
		}
		if finishedAt.Valid {
			jr.FinishedAt = &finishedAt.Time
		}
		if failureReason.Valid {
			jr.FailureReason = failureReason.String
		}

		runs = append(runs, &jr)
	}
	return runs, nil
}

// ListJobRunsByService retrieves job runs for a service.
func (s *Store) ListJobRunsByService(serviceID string) ([]*api.JobRun, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, cron_job_id, type, name, command, status, exit_code, started_at, finished_at, logs, failure_reason
		FROM jobs WHERE service_id = ? ORDER BY started_at DESC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*api.JobRun
	for rows.Next() {
		var jr api.JobRun
		var cronJobID sql.NullString
		var exitCode sql.NullInt64
		var finishedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&jr.ID, &jr.ServiceID, &cronJobID, &jr.Type, &jr.Name, &jr.Command, &jr.Status, &exitCode, &jr.StartedAt, &finishedAt, &jr.Logs, &failureReason)
		if err != nil {
			return nil, err
		}

		jr.CronJobID = scanStringPtr(cronJobID)
		if exitCode.Valid {
			val := int(exitCode.Int64)
			jr.ExitCode = &val
		}
		if finishedAt.Valid {
			jr.FinishedAt = &finishedAt.Time
		}
		if failureReason.Valid {
			jr.FailureReason = failureReason.String
		}

		runs = append(runs, &jr)
	}
	return runs, nil
}

// ListJobRunsByCronJob retrieves job runs for a specific cron job.
func (s *Store) ListJobRunsByCronJob(cronJobID string) ([]*api.JobRun, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, cron_job_id, type, name, command, status, exit_code, started_at, finished_at, logs, failure_reason
		FROM jobs WHERE cron_job_id = ? ORDER BY started_at DESC`, cronJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*api.JobRun
	for rows.Next() {
		var jr api.JobRun
		var cronJobID sql.NullString
		var exitCode sql.NullInt64
		var finishedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&jr.ID, &jr.ServiceID, &cronJobID, &jr.Type, &jr.Name, &jr.Command, &jr.Status, &exitCode, &jr.StartedAt, &finishedAt, &jr.Logs, &failureReason)
		if err != nil {
			return nil, err
		}

		jr.CronJobID = scanStringPtr(cronJobID)
		if exitCode.Valid {
			val := int(exitCode.Int64)
			jr.ExitCode = &val
		}
		if finishedAt.Valid {
			jr.FinishedAt = &finishedAt.Time
		}
		if failureReason.Valid {
			jr.FailureReason = failureReason.String
		}

		runs = append(runs, &jr)
	}
	return runs, nil
}

// DeleteJobRun deletes a job run by ID.
func (s *Store) DeleteJobRun(id string) error {
	_, err := s.db.Exec("DELETE FROM jobs WHERE id = ?", id)
	return err
}
