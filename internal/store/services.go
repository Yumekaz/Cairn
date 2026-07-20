package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// --- Services CRUD ---

// GetService retrieves a service by ID.
func (s *Store) GetService(id string) (*api.Service, error) {
	row := s.db.QueryRow(`
		SELECT id, name, kind, runtime_backend, runtime_id, current_deploy_id, desired_state, actual_state, route, created_at, updated_at
		FROM services WHERE id = ?`, id)

	var svc api.Service
	err := row.Scan(&svc.ID, &svc.Name, &svc.Kind, &svc.RuntimeBackend, &svc.RuntimeID, &svc.CurrentDeployID, &svc.DesiredState, &svc.ActualState, &svc.Route, &svc.CreatedAt, &svc.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &svc, nil
}

// GetServiceByName retrieves a service by name.
func (s *Store) GetServiceByName(name string) (*api.Service, error) {
	row := s.db.QueryRow(`
		SELECT id, name, kind, runtime_backend, runtime_id, current_deploy_id, desired_state, actual_state, route, created_at, updated_at
		FROM services WHERE name = ?`, name)

	var svc api.Service
	err := row.Scan(&svc.ID, &svc.Name, &svc.Kind, &svc.RuntimeBackend, &svc.RuntimeID, &svc.CurrentDeployID, &svc.DesiredState, &svc.ActualState, &svc.Route, &svc.CreatedAt, &svc.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &svc, nil
}

// ListServices retrieves all services.
func (s *Store) ListServices() ([]*api.Service, error) {
	rows, err := s.db.Query(`
		SELECT id, name, kind, runtime_backend, runtime_id, current_deploy_id, desired_state, actual_state, route, created_at, updated_at
		FROM services ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var svcs []*api.Service
	for rows.Next() {
		var svc api.Service
		err := rows.Scan(&svc.ID, &svc.Name, &svc.Kind, &svc.RuntimeBackend, &svc.RuntimeID, &svc.CurrentDeployID, &svc.DesiredState, &svc.ActualState, &svc.Route, &svc.CreatedAt, &svc.UpdatedAt)
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, &svc)
	}
	return svcs, nil
}

// UpsertService creates or updates a service.
func (s *Store) UpsertService(svc *api.Service) error {
	now := time.Now()
	svc.UpdatedAt = now
	if svc.CreatedAt.IsZero() {
		svc.CreatedAt = now
	}

	_, err := s.db.Exec(`
		INSERT INTO services (id, name, kind, runtime_backend, runtime_id, current_deploy_id, desired_state, actual_state, route, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			runtime_backend = excluded.runtime_backend,
			runtime_id = excluded.runtime_id,
			current_deploy_id = excluded.current_deploy_id,
			desired_state = excluded.desired_state,
			actual_state = excluded.actual_state,
			route = excluded.route,
			updated_at = excluded.updated_at`,
		svc.ID, svc.Name, svc.Kind, svc.RuntimeBackend, svc.RuntimeID, svc.CurrentDeployID, svc.DesiredState, svc.ActualState, svc.Route, svc.CreatedAt, svc.UpdatedAt)
	return err
}

// DeleteService deletes a service by ID.
func (s *Store) DeleteService(id string) error {
	_, err := s.db.Exec("DELETE FROM services WHERE id = ?", id)
	return err
}

// --- Deploys CRUD ---

// CreateDeploy creates a new deploy record.
func (s *Store) CreateDeploy(d *api.Deploy) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}

	var completedAt interface{}
	if d.CompletedAt != nil {
		completedAt = *d.CompletedAt
	}

	_, err := s.db.Exec(`
		INSERT INTO deploys (id, service_id, version, source_path, status, stage, health_status, previous_deploy_id, state_touched, created_at, completed_at, failure_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ServiceID, d.Version, d.SourcePath, d.Status, d.Stage, d.HealthStatus, d.PreviousDeployID, d.StateTouched, d.CreatedAt, completedAt, d.FailureReason)
	return err
}

// GetDeploy retrieves a deploy record by ID.
func (s *Store) GetDeploy(id string) (*api.Deploy, error) {
	row := s.db.QueryRow(`
		SELECT id, service_id, version, source_path, status, stage, health_status, previous_deploy_id, state_touched, created_at, completed_at, failure_reason
		FROM deploys WHERE id = ?`, id)

	var d api.Deploy
	var completedAt sql.NullTime
	var failureReason sql.NullString

	err := row.Scan(&d.ID, &d.ServiceID, &d.Version, &d.SourcePath, &d.Status, &d.Stage, &d.HealthStatus, &d.PreviousDeployID, &d.StateTouched, &d.CreatedAt, &completedAt, &failureReason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if completedAt.Valid {
		d.CompletedAt = &completedAt.Time
	}
	if failureReason.Valid {
		d.FailureReason = failureReason.String
	}

	return &d, nil
}

// ListDeploys retrieves all deploys for a service.
func (s *Store) ListDeploys(serviceID string) ([]*api.Deploy, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, version, source_path, status, stage, health_status, previous_deploy_id, state_touched, created_at, completed_at, failure_reason
		FROM deploys WHERE service_id = ? ORDER BY created_at DESC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deploys []*api.Deploy
	for rows.Next() {
		var d api.Deploy
		var completedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&d.ID, &d.ServiceID, &d.Version, &d.SourcePath, &d.Status, &d.Stage, &d.HealthStatus, &d.PreviousDeployID, &d.StateTouched, &d.CreatedAt, &completedAt, &failureReason)
		if err != nil {
			return nil, err
		}

		if completedAt.Valid {
			d.CompletedAt = &completedAt.Time
		}
		if failureReason.Valid {
			d.FailureReason = failureReason.String
		}

		deploys = append(deploys, &d)
	}
	return deploys, nil
}

// UpdateDeploy updates an existing deploy.
func (s *Store) UpdateDeploy(d *api.Deploy) error {
	var completedAt interface{}
	if d.CompletedAt != nil {
		completedAt = *d.CompletedAt
	}

	res, err := s.db.Exec(`
		UPDATE deploys SET
			status = ?,
			stage = ?,
			health_status = ?,
			state_touched = ?,
			completed_at = ?,
			failure_reason = ?
		WHERE id = ?`,
		d.Status, d.Stage, d.HealthStatus, d.StateTouched, completedAt, d.FailureReason, d.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("UpdateDeploy: no rows updated for id %s", d.ID)
	}
	return nil
}

// MarkDeployTerminal forces a deploy into a terminal status by id.
func (s *Store) MarkDeployTerminal(id, status, health, failureReason string) error {
	now := time.Now()
	res, err := s.db.Exec(`
		UPDATE deploys SET status = ?, stage = 'completed', health_status = ?,
			completed_at = ?, failure_reason = ?
		WHERE id = ?`, status, health, now, failureReason, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("MarkDeployTerminal: no rows for id %s", id)
	}
	// Ensure readers on other connections see the write promptly.
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	var got string
	if err := s.db.QueryRow(`SELECT status FROM deploys WHERE id = ?`, id).Scan(&got); err != nil {
		return err
	}
	if got != status {
		return fmt.Errorf("MarkDeployTerminal: read-back status %q want %q for id %s", got, status, id)
	}
	return nil
}

// --- Volumes CRUD ---

// GetVolume retrieves a volume by ID.
func (s *Store) GetVolume(id string) (*api.Volume, error) {
	row := s.db.QueryRow(`
		SELECT id, name, host_path, attached_service_id, mount_path, status, created_at, updated_at
		FROM volumes WHERE id = ?`, id)

	var v api.Volume
	var attachedSvc sql.NullString
	err := row.Scan(&v.ID, &v.Name, &v.HostPath, &attachedSvc, &v.MountPath, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.AttachedServiceID = scanString(attachedSvc)
	return &v, nil
}

// GetVolumeByName retrieves a volume by Name.
func (s *Store) GetVolumeByName(name string) (*api.Volume, error) {
	row := s.db.QueryRow(`
		SELECT id, name, host_path, attached_service_id, mount_path, status, created_at, updated_at
		FROM volumes WHERE name = ?`, name)

	var v api.Volume
	var attachedSvc sql.NullString
	err := row.Scan(&v.ID, &v.Name, &v.HostPath, &attachedSvc, &v.MountPath, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.AttachedServiceID = scanString(attachedSvc)
	return &v, nil
}

// ListVolumes retrieves all volumes.
func (s *Store) ListVolumes() ([]*api.Volume, error) {
	rows, err := s.db.Query(`
		SELECT id, name, host_path, attached_service_id, mount_path, status, created_at, updated_at
		FROM volumes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vols []*api.Volume
	for rows.Next() {
		var v api.Volume
		var attachedSvc sql.NullString
		err := rows.Scan(&v.ID, &v.Name, &v.HostPath, &attachedSvc, &v.MountPath, &v.Status, &v.CreatedAt, &v.UpdatedAt)
		if err != nil {
			return nil, err
		}
		v.AttachedServiceID = scanString(attachedSvc)
		vols = append(vols, &v)
	}
	return vols, nil
}

// UpsertVolume creates or updates a volume.
func (s *Store) UpsertVolume(v *api.Volume) error {
	now := time.Now()
	v.UpdatedAt = now
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}

	var attachedSvc interface{}
	if v.AttachedServiceID != "" {
		attachedSvc = v.AttachedServiceID
	}

	_, err := s.db.Exec(`
		INSERT INTO volumes (id, name, host_path, attached_service_id, mount_path, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			host_path = excluded.host_path,
			attached_service_id = excluded.attached_service_id,
			mount_path = excluded.mount_path,
			status = excluded.status,
			updated_at = excluded.updated_at`,
		v.ID, v.Name, v.HostPath, attachedSvc, v.MountPath, v.Status, v.CreatedAt, v.UpdatedAt)
	return err
}

// DeleteVolume deletes a volume by ID.
func (s *Store) DeleteVolume(id string) error {
	_, err := s.db.Exec("DELETE FROM volumes WHERE id = ?", id)
	return err
}

// --- Backups CRUD ---

// CreateBackup creates a new backup record.
func (s *Store) CreateBackup(b *api.Backup) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now()
	}

	var completedAt interface{}
	if b.CompletedAt != nil {
		completedAt = *b.CompletedAt
	}

	_, err := s.db.Exec(`
		INSERT INTO backups (id, volume_id, backup_path, status, size_bytes, checksum, created_at, completed_at, failure_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.VolumeID, b.BackupPath, b.Status, b.SizeBytes, b.Checksum, b.CreatedAt, completedAt, b.FailureReason)
	return err
}

// GetBackup retrieves a backup record by ID.
func (s *Store) GetBackup(id string) (*api.Backup, error) {
	row := s.db.QueryRow(`
		SELECT id, volume_id, backup_path, status, size_bytes, checksum, created_at, completed_at, failure_reason
		FROM backups WHERE id = ?`, id)

	var b api.Backup
	var completedAt sql.NullTime
	var failureReason sql.NullString

	err := row.Scan(&b.ID, &b.VolumeID, &b.BackupPath, &b.Status, &b.SizeBytes, &b.Checksum, &b.CreatedAt, &completedAt, &failureReason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if completedAt.Valid {
		b.CompletedAt = &completedAt.Time
	}
	if failureReason.Valid {
		b.FailureReason = failureReason.String
	}

	return &b, nil
}

// ListBackups retrieves all backups for a volume.
func (s *Store) ListBackups(volumeID string) ([]*api.Backup, error) {
	rows, err := s.db.Query(`
		SELECT id, volume_id, backup_path, status, size_bytes, checksum, created_at, completed_at, failure_reason
		FROM backups WHERE volume_id = ? ORDER BY created_at DESC`, volumeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backups []*api.Backup
	for rows.Next() {
		var b api.Backup
		var completedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&b.ID, &b.VolumeID, &b.BackupPath, &b.Status, &b.SizeBytes, &b.Checksum, &b.CreatedAt, &completedAt, &failureReason)
		if err != nil {
			return nil, err
		}

		if completedAt.Valid {
			b.CompletedAt = &completedAt.Time
		}
		if failureReason.Valid {
			b.FailureReason = failureReason.String
		}

		backups = append(backups, &b)
	}
	return backups, nil
}

// UpdateBackup updates an existing backup.
func (s *Store) UpdateBackup(b *api.Backup) error {
	var completedAt interface{}
	if b.CompletedAt != nil {
		completedAt = *b.CompletedAt
	}

	_, err := s.db.Exec(`
		UPDATE backups SET
			status = ?,
			size_bytes = ?,
			checksum = ?,
			completed_at = ?,
			failure_reason = ?
		WHERE id = ?`,
		b.Status, b.SizeBytes, b.Checksum, completedAt, b.FailureReason, b.ID)
	return err
}

// ListIncompleteBackups returns backups that are not terminal (not success/failed).
// These are left after a mid-backup process death and must be failed on recovery.
func (s *Store) ListIncompleteBackups() ([]*api.Backup, error) {
	rows, err := s.db.Query(`
		SELECT id, volume_id, backup_path, status, size_bytes, checksum, created_at, completed_at, failure_reason
		FROM backups
		WHERE status NOT IN ('success', 'failed')
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backups []*api.Backup
	for rows.Next() {
		var b api.Backup
		var completedAt sql.NullTime
		var failureReason sql.NullString

		err := rows.Scan(&b.ID, &b.VolumeID, &b.BackupPath, &b.Status, &b.SizeBytes, &b.Checksum, &b.CreatedAt, &completedAt, &failureReason)
		if err != nil {
			return nil, err
		}
		if completedAt.Valid {
			b.CompletedAt = &completedAt.Time
		}
		if failureReason.Valid {
			b.FailureReason = failureReason.String
		}
		backups = append(backups, &b)
	}
	return backups, nil
}

// FailIncompleteBackups marks every non-terminal backup as failed.
// Returns the number of rows updated. Safe to call on every daemon start.
func (s *Store) FailIncompleteBackups(reason string) (int, error) {
	if reason == "" {
		reason = "interrupted (daemon restart)"
	}
	now := time.Now()
	res, err := s.db.Exec(`
		UPDATE backups
		SET status = 'failed',
		    completed_at = ?,
		    failure_reason = ?
		WHERE status NOT IN ('success', 'failed')`,
		now, reason)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListActiveDeployIDs returns all deploy IDs that are not completed (status != success and status != failed).
func (s *Store) ListActiveDeployIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM deploys WHERE status != 'success' AND status != 'failed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ServiceHasActiveDeploy reports whether the service has a non-terminal deploy in flight.
// Used by reconciliation so pending candidates (which are not current_deploy_id) still block recreate.
func (s *Store) ServiceHasActiveDeploy(serviceID string) (bool, error) {
	row := s.db.QueryRow(`
		SELECT COUNT(1) FROM deploys
		WHERE service_id = ? AND status != 'success' AND status != 'failed'`, serviceID)
	var n int
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
