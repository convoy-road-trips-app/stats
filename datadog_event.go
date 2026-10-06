package stats

import "github.com/convoy-road-trips-app/stats/models"

// Re-export the Datadog event types from the models package.
type (
	// DatadogEvent is a Datadog event sent over DogStatsD.
	DatadogEvent = models.DatadogEvent
	// EventPriority is the priority of a Datadog event.
	EventPriority = models.EventPriority
	// EventAlertType is the alert type of a Datadog event.
	EventAlertType = models.EventAlertType
)

const (
	// EventPriorityNormal is the default Datadog event priority.
	EventPriorityNormal = models.EventPriorityNormal
	// EventPriorityLow marks a low priority Datadog event.
	EventPriorityLow = models.EventPriorityLow
	// EventAlertTypeError marks an error event.
	EventAlertTypeError = models.EventAlertTypeError
	// EventAlertTypeWarning marks a warning event.
	EventAlertTypeWarning = models.EventAlertTypeWarning
	// EventAlertTypeInfo marks an informational event.
	EventAlertTypeInfo = models.EventAlertTypeInfo
	// EventAlertTypeSuccess marks a success event.
	EventAlertTypeSuccess = models.EventAlertTypeSuccess
)
