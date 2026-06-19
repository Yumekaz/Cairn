package events

// EventType represents the category of the audit event.
type EventType string

const (
	// Daemon Events
	DaemonStarted EventType = "DaemonStarted"
	DaemonStopped EventType = "DaemonStopped"

	// Service Events
	ServiceCreated   EventType = "ServiceCreated"
	ServiceStarted   EventType = "ServiceStarted"
	ServiceStopped   EventType = "ServiceStopped"
	ServiceRestarted EventType = "ServiceRestarted"
	ServiceRemoved   EventType = "ServiceRemoved"

	// Deploy Events
	DeployStarted   EventType = "DeployStarted"
	DeploySucceeded EventType = "DeploySucceeded"
	DeployFailed    EventType = "DeployFailed"

	// Volume Events
	VolumeCreated EventType = "VolumeCreated"
	VolumeRemoved EventType = "VolumeRemoved"

	// Backup Events
	BackupStarted   EventType = "BackupStarted"
	BackupSucceeded EventType = "BackupSucceeded"
	BackupFailed    EventType = "BackupFailed"
	BackupRestored  EventType = "BackupRestored"
)

// String returns the string representation of the event type.
func (e EventType) String() string {
	return string(e)
}
