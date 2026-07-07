package store

import (
	"github.com/yumekaz/cairn/internal/api"
)

// UpsertServiceEnv inserts or updates an environment variable / secret.
func (s *Store) UpsertServiceEnv(env *api.ServiceEnvVar) error {
	_, err := s.db.Exec(`
		INSERT INTO service_envs (service_id, key, value, is_secret)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(service_id, key) DO UPDATE SET
			value = excluded.value,
			is_secret = excluded.is_secret`,
		env.ServiceID, env.Key, env.Value, env.IsSecret)
	return err
}

// ListServiceEnvs retrieves all environment variables and secrets for a service.
func (s *Store) ListServiceEnvs(serviceID string) ([]*api.ServiceEnvVar, error) {
	rows, err := s.db.Query(`
		SELECT service_id, key, value, is_secret
		FROM service_envs WHERE service_id = ? ORDER BY key ASC`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []*api.ServiceEnvVar
	for rows.Next() {
		var env api.ServiceEnvVar
		if err := rows.Scan(&env.ServiceID, &env.Key, &env.Value, &env.IsSecret); err != nil {
			return nil, err
		}
		envs = append(envs, &env)
	}
	return envs, nil
}

// DeleteServiceEnv deletes a specific environment variable for a service.
func (s *Store) DeleteServiceEnv(serviceID string, key string) error {
	_, err := s.db.Exec(`DELETE FROM service_envs WHERE service_id = ? AND key = ?`, serviceID, key)
	return err
}
