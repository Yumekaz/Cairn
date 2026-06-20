package api

import (
	"time"
)

// Service represents a registered service in the Cairn registry.
type Service struct {
	ID               string    `json:"id" db:"id"`
	Name             string    `json:"name" db:"name"`
	Kind             string    `json:"kind" db:"kind"`
	RuntimeBackend   string    `json:"runtime_backend" db:"runtime_backend"`
	RuntimeID        string    `json:"runtime_id" db:"runtime_id"`
	CurrentDeployID  string    `json:"current_deploy_id" db:"current_deploy_id"`
	DesiredState     string    `json:"desired_state" db:"desired_state"`
	ActualState      string    `json:"actual_state" db:"actual_state"`
	Route            string    `json:"route" db:"route"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// Deploy represents a deploy record for a service.
type Deploy struct {
	ID               string     `json:"id" db:"id"`
	ServiceID        string     `json:"service_id" db:"service_id"`
	Version          string     `json:"version" db:"version"`
	SourcePath       string     `json:"source_path" db:"source_path"`
	Status           string     `json:"status" db:"status"` // e.g., pending, running, success, failed
	Stage            string     `json:"stage" db:"stage"`
	HealthStatus     string     `json:"health_status" db:"health_status"`
	PreviousDeployID string     `json:"previous_deploy_id" db:"previous_deploy_id"`
	StateTouched     bool       `json:"state_touched" db:"state_touched"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	FailureReason    string     `json:"failure_reason,omitempty" db:"failure_reason"`
}

// Volume represents a persistent volume configuration.
type Volume struct {
	ID                string    `json:"id" db:"id"`
	Name              string    `json:"name" db:"name"`
	HostPath          string    `json:"host_path" db:"host_path"`
	AttachedServiceID string    `json:"attached_service_id" db:"attached_service_id"`
	MountPath         string    `json:"mount_path" db:"mount_path"`
	Status            string    `json:"status" db:"status"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// Backup represents a snapshot of a volume.
type Backup struct {
	ID            string     `json:"id" db:"id"`
	VolumeID      string     `json:"volume_id" db:"volume_id"`
	BackupPath    string     `json:"backup_path" db:"backup_path"`
	Status        string     `json:"status" db:"status"`
	SizeBytes     int64      `json:"size_bytes" db:"size_bytes"`
	Checksum      string     `json:"checksum" db:"checksum"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	FailureReason string     `json:"failure_reason,omitempty" db:"failure_reason"`
}

// Event represents an audit event.
type Event struct {
	ID           string    `json:"id" db:"id"`
	ServiceID    *string   `json:"service_id,omitempty" db:"service_id"`
	DeployID     *string   `json:"deploy_id,omitempty" db:"deploy_id"`
	VolumeID     *string   `json:"volume_id,omitempty" db:"volume_id"`
	BackupID     *string   `json:"backup_id,omitempty" db:"backup_id"`
	Type         string    `json:"type" db:"type"`
	Message      string    `json:"message" db:"message"`
	MetadataJSON string    `json:"metadata_json" db:"metadata_json"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// ServiceConfig represents the parsed configuration of a service (from cairn.yaml).
type ServiceConfig struct {
	Name        string             `yaml:"name" json:"name"`
	Kind        string             `yaml:"kind" json:"kind"`
	Image       string             `yaml:"image" json:"image"`
	Command     []string           `yaml:"command,omitempty" json:"command,omitempty"`
	Migration   string             `yaml:"migration,omitempty" json:"migration,omitempty"`
	Environment map[string]string  `yaml:"environment,omitempty" json:"environment,omitempty"`
	Ports       []PortMapping      `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes     []VolumeConfig     `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	HealthCheck *HealthCheckConfig `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Restart     *RestartConfig     `yaml:"restart,omitempty" json:"restart,omitempty"`
	Schedule    string             `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	Run         string             `yaml:"run,omitempty" json:"run,omitempty"`
}

type PortMapping struct {
	Host      int `yaml:"host" json:"host"`
	Container int `yaml:"container" json:"container"`
}

type VolumeConfig struct {
	Name      string `yaml:"name" json:"name"`
	MountPath string `yaml:"mount_path" json:"mount_path"`
}

type HealthCheckConfig struct {
	HTTPPath     string        `yaml:"http_path" json:"http_path"`
	Interval     time.Duration `yaml:"interval" json:"interval"`
	Timeout      time.Duration `yaml:"timeout" json:"timeout"`
	Retries      int           `yaml:"retries" json:"retries"`
	StartupGrace time.Duration `yaml:"startup_grace" json:"startup_grace"`
}

type RestartConfig struct {
	Policy     string `yaml:"policy" json:"policy"`
	MaxRetries int    `yaml:"max_retries" json:"max_retries"`
}

// CronJob represents a scheduled cron job configuration.
type CronJob struct {
	ID        string    `json:"id" db:"id"`
	ServiceID string    `json:"service_id" db:"service_id"`
	Name      string    `json:"name" db:"name"`
	Schedule  string    `json:"schedule" db:"schedule"`
	Command   string    `json:"command" db:"command"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// JobRun represents a recorded execution of a cron job or one-off run.
type JobRun struct {
	ID            string     `json:"id" db:"id"`
	ServiceID     string     `json:"service_id" db:"service_id"`
	CronJobID     *string    `json:"cron_job_id,omitempty" db:"cron_job_id"`
	Type          string     `json:"type" db:"type"` // e.g., "one-off", "cron"
	Name          string     `json:"name" db:"name"` // run name or command
	Command       string     `json:"command" db:"command"`
	Status        string     `json:"status" db:"status"` // pending, running, success, failed
	ExitCode      *int       `json:"exit_code,omitempty" db:"exit_code"`
	StartedAt     time.Time  `json:"started_at" db:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	Logs          string     `json:"logs,omitempty" db:"logs"`
	FailureReason string     `json:"failure_reason,omitempty" db:"failure_reason"`
}

// DaemonStatus represents the current state of the cairnd daemon.
type DaemonStatus struct {
	Uptime         string `json:"uptime"`
	Version        string `json:"version"`
	ActiveServices int    `json:"active_services"`
	StorageUsage   string `json:"storage_usage"`
}
