package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// CreateCronJob creates a new cron job.
func (s *Store) CreateCronJob(cj *api.CronJob) error {
	now := time.Now()
	cj.UpdatedAt = now
	if cj.CreatedAt.IsZero() {
		cj.CreatedAt = now
	}

	_, err := s.db.Exec(`
		INSERT INTO cron_jobs (id, service_id, name, schedule, command, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cj.ID, cj.ServiceID, cj.Name, cj.Schedule, cj.Command, cj.CreatedAt, cj.UpdatedAt)
	return err
}

// UpsertCronJob inserts or updates a cron job.
func (s *Store) UpsertCronJob(cj *api.CronJob) error {
	now := time.Now()
	cj.UpdatedAt = now
	if cj.CreatedAt.IsZero() {
		cj.CreatedAt = now
	}

	_, err := s.db.Exec(`
		INSERT INTO cron_jobs (id, service_id, name, schedule, command, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			service_id = excluded.service_id,
			schedule = excluded.schedule,
			command = excluded.command,
			updated_at = excluded.updated_at`,
		cj.ID, cj.ServiceID, cj.Name, cj.Schedule, cj.Command, cj.CreatedAt, cj.UpdatedAt)
	return err
}

// GetCronJob retrieves a cron job by ID.
func (s *Store) GetCronJob(id string) (*api.CronJob, error) {
	row := s.db.QueryRow(`
		SELECT id, service_id, name, schedule, command, created_at, updated_at
		FROM cron_jobs WHERE id = ?`, id)

	var cj api.CronJob
	err := row.Scan(&cj.ID, &cj.ServiceID, &cj.Name, &cj.Schedule, &cj.Command, &cj.CreatedAt, &cj.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cj, nil
}

// GetCronJobByName retrieves a cron job by name.
func (s *Store) GetCronJobByName(name string) (*api.CronJob, error) {
	row := s.db.QueryRow(`
		SELECT id, service_id, name, schedule, command, created_at, updated_at
		FROM cron_jobs WHERE name = ?`, name)

	var cj api.CronJob
	err := row.Scan(&cj.ID, &cj.ServiceID, &cj.Name, &cj.Schedule, &cj.Command, &cj.CreatedAt, &cj.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cj, nil
}

// ListCronJobs retrieves all cron jobs.
func (s *Store) ListCronJobs() ([]*api.CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, name, schedule, command, created_at, updated_at
		FROM cron_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*api.CronJob
	for rows.Next() {
		var cj api.CronJob
		err := rows.Scan(&cj.ID, &cj.ServiceID, &cj.Name, &cj.Schedule, &cj.Command, &cj.CreatedAt, &cj.UpdatedAt)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &cj)
	}
	return jobs, nil
}

// ListCronJobsByService retrieves all cron jobs for a specific service.
func (s *Store) ListCronJobsByService(serviceID string) ([]*api.CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, name, schedule, command, created_at, updated_at
		FROM cron_jobs WHERE service_id = ? ORDER BY created_at DESC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*api.CronJob
	for rows.Next() {
		var cj api.CronJob
		err := rows.Scan(&cj.ID, &cj.ServiceID, &cj.Name, &cj.Schedule, &cj.Command, &cj.CreatedAt, &cj.UpdatedAt)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &cj)
	}
	return jobs, nil
}

// DeleteCronJob deletes a cron job by ID.
func (s *Store) DeleteCronJob(id string) error {
	_, err := s.db.Exec("DELETE FROM cron_jobs WHERE id = ?", id)
	return err
}

// DeleteCronJobByName deletes a cron job by name.
func (s *Store) DeleteCronJobByName(name string) error {
	_, err := s.db.Exec("DELETE FROM cron_jobs WHERE name = ?", name)
	return err
}
