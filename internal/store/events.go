package store

import (
	"database/sql"
	"time"

	"github.com/yumekaz/cairn/internal/api"
)

// EventFilter specifies optional criteria for querying event logs.
type EventFilter struct {
	ServiceID *string
	DeployID  *string
	Limit     int
}

// CreateEvent writes an audit event to the SQLite database.
func (s *Store) CreateEvent(e *api.Event) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}

	_, err := s.db.Exec(`
		INSERT INTO events (id, service_id, deploy_id, volume_id, backup_id, type, message, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, toNullString(e.ServiceID), toNullString(e.DeployID), toNullString(e.VolumeID), toNullString(e.BackupID), e.Type, e.Message, e.MetadataJSON, e.CreatedAt)
	return err
}

// ListEvents retrieves events matching the filter.
func (s *Store) ListEvents(filter EventFilter) ([]*api.Event, error) {
	query := `
		SELECT id, service_id, deploy_id, volume_id, backup_id, type, message, metadata_json, created_at
		FROM events WHERE 1=1`
	var args []interface{}

	if filter.ServiceID != nil {
		query += " AND service_id = ?"
		args = append(args, *filter.ServiceID)
	}
	if filter.DeployID != nil {
		query += " AND deploy_id = ?"
		args = append(args, *filter.DeployID)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*api.Event
	for rows.Next() {
		var e api.Event
		var serviceID, deployID, volumeID, backupID sql.NullString

		err := rows.Scan(&e.ID, &serviceID, &deployID, &volumeID, &backupID, &e.Type, &e.Message, &e.MetadataJSON, &e.CreatedAt)
		if err != nil {
			return nil, err
		}

		e.ServiceID = scanStringPtr(serviceID)
		e.DeployID = scanStringPtr(deployID)
		e.VolumeID = scanStringPtr(volumeID)
		e.BackupID = scanStringPtr(backupID)

		events = append(events, &e)
	}
	return events, nil
}
