package store

import (
	"database/sql"
)

// Migrate runs migrations to initialize the database schema.
func (s *Store) Migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS services (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			runtime_backend TEXT NOT NULL,
			runtime_id TEXT NOT NULL,
			current_deploy_id TEXT NOT NULL,
			desired_state TEXT NOT NULL,
			actual_state TEXT NOT NULL,
			route TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS deploys (
			id TEXT PRIMARY KEY,
			service_id TEXT NOT NULL,
			version TEXT NOT NULL,
			source_path TEXT NOT NULL,
			status TEXT NOT NULL,
			stage TEXT NOT NULL,
			health_status TEXT NOT NULL,
			previous_deploy_id TEXT NOT NULL,
			state_touched BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			completed_at DATETIME,
			failure_reason TEXT,
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
		);`,

		`CREATE TABLE IF NOT EXISTS volumes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			host_path TEXT NOT NULL,
			attached_service_id TEXT,
			mount_path TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(attached_service_id) REFERENCES services(id) ON DELETE SET NULL
		);`,

		`CREATE TABLE IF NOT EXISTS backups (
			id TEXT PRIMARY KEY,
			volume_id TEXT NOT NULL,
			backup_path TEXT NOT NULL,
			status TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			checksum TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			completed_at DATETIME,
			failure_reason TEXT,
			FOREIGN KEY(volume_id) REFERENCES volumes(id) ON DELETE CASCADE
		);`,

		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			service_id TEXT,
			deploy_id TEXT,
			volume_id TEXT,
			backup_id TEXT,
			type TEXT NOT NULL,
			message TEXT NOT NULL,
			metadata_json TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS cron_jobs (
			id TEXT PRIMARY KEY,
			service_id TEXT NOT NULL,
			name TEXT NOT NULL UNIQUE,
			schedule TEXT NOT NULL,
			command TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
		);`,

		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			service_id TEXT NOT NULL,
			cron_job_id TEXT,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			command TEXT NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER,
			started_at DATETIME NOT NULL,
			finished_at DATETIME,
			logs TEXT NOT NULL,
			failure_reason TEXT,
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE,
			FOREIGN KEY(cron_job_id) REFERENCES cron_jobs(id) ON DELETE SET NULL
		);`,
	}

	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return err
		}
	}

	return nil
}

// Helper function to scan nullable string.
func scanString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// Helper function to scan nullable string pointer.
func scanStringPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

// Helper function to convert string pointer to nullable sql string.
func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}
