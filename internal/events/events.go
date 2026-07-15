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
	// DeployCompleted is an MLP §17 alias of DeploySucceeded (same string family for greps).
	DeployCompleted EventType = "DeployCompleted"
	DeployFailed    EventType = "DeployFailed"

	// Runtime / health / routing (MLP §17)
	RuntimeCreateStarted   EventType = "RuntimeCreateStarted"
	RuntimeCreateCompleted EventType = "RuntimeCreateCompleted"
	HealthCheckPassed      EventType = "HealthCheckPassed"
	HealthCheckFailed      EventType = "HealthCheckFailed"
	RouteUpdated           EventType = "RouteUpdated"
	RoutePreserved         EventType = "RoutePreserved"

	// Volume Events
	VolumeCreated  EventType = "VolumeCreated"
	VolumeAttached EventType = "VolumeAttached"
	VolumeRemoved  EventType = "VolumeRemoved"

	// Backup Events
	BackupStarted   EventType = "BackupStarted"
	BackupSucceeded EventType = "BackupSucceeded"
	// BackupCompleted is an MLP §17 alias name for BackupSucceeded.
	BackupCompleted EventType = "BackupCompleted"
	BackupFailed    EventType = "BackupFailed"
	BackupRestored  EventType = "BackupRestored"

	// Restore Events (MLP §17)
	RestoreStarted   EventType = "RestoreStarted"
	RestoreCompleted EventType = "RestoreCompleted"
	RestoreFailed    EventType = "RestoreFailed"
)

// String returns the string representation of the event type.
func (e EventType) String() string {
	return string(e)
}

// ImplementedTypes lists event types the control plane is expected to emit.
// Aliases (DeployCompleted, BackupCompleted) are emitted alongside canonical names
// only where greps need both; otherwise see docs/events.md.
func ImplementedTypes() []EventType {
	return []EventType{
		DaemonStarted, DaemonStopped,
		ServiceCreated, ServiceStarted, ServiceStopped, ServiceRestarted, ServiceRemoved,
		DeployStarted, DeploySucceeded, DeployCompleted, DeployFailed,
		RuntimeCreateStarted, RuntimeCreateCompleted,
		HealthCheckPassed, HealthCheckFailed,
		RouteUpdated, RoutePreserved,
		VolumeCreated, VolumeAttached, VolumeRemoved,
		BackupStarted, BackupSucceeded, BackupCompleted, BackupFailed, BackupRestored,
		RestoreStarted, RestoreCompleted, RestoreFailed,
	}
}
