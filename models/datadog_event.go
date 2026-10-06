package models

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// EventPriority is the priority of a Datadog event.
type EventPriority string

const (
	// EventPriorityNormal is the default Datadog event priority.
	EventPriorityNormal EventPriority = "normal"
	// EventPriorityLow marks a low priority Datadog event.
	EventPriorityLow EventPriority = "low"
)

// EventAlertType is the alert type of a Datadog event.
type EventAlertType string

const (
	// EventAlertTypeError marks an error event.
	EventAlertTypeError EventAlertType = "error"
	// EventAlertTypeWarning marks a warning event.
	EventAlertTypeWarning EventAlertType = "warning"
	// EventAlertTypeInfo marks an informational event.
	EventAlertTypeInfo EventAlertType = "info"
	// EventAlertTypeSuccess marks a success event.
	EventAlertTypeSuccess EventAlertType = "success"
)

// DatadogEvent is a Datadog event sent over DogStatsD. Title and Text are
// required by the protocol; every other field is optional and omitted from the
// wire format when empty (a zero Timestamp is omitted too).
type DatadogEvent struct {
	Title          string
	Text           string
	Timestamp      time.Time
	Host           string
	Priority       EventPriority
	AlertType      EventAlertType
	AggregationKey string
	SourceTypeName string
	Tags           []attribute.KeyValue
}
