package api

// RecoveryStatus is observational; it never clears uncertainty or restores data.
type RecoveryStatus struct {
	Service         string               `json:"service"`
	CurrentDeployID string               `json:"current_deploy_id"`
	Deployments     []RecoveryDeployment `json:"deployments"`
	NextActions     []string             `json:"next_actions"`
}

type RecoveryDeployment struct {
	DeployID    string           `json:"deploy_id"`
	Status      string           `json:"status"`
	Reason      string           `json:"reason"`
	TaskName    string           `json:"task_name"`
	TaskState   string           `json:"task_state"`
	Volumes     []RecoveryVolume `json:"volumes"`
	ConfigError string           `json:"config_error,omitempty"`
}

type RecoveryVolume struct {
	Name    string    `json:"name"`
	Backups []*Backup `json:"backups"`
}
