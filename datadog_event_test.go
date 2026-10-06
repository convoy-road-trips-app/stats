package stats

import (
	"testing"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
)

func TestDatadogEventReexports(t *testing.T) {
	e := DatadogEvent{Title: "t", Priority: EventPriorityLow, AlertType: EventAlertTypeWarning}
	var m = e
	assert.Equal(t, models.EventPriorityLow, m.Priority)
	assert.Equal(t, "normal", string(EventPriorityNormal))
	assert.Equal(t, "low", string(EventPriorityLow))
	assert.Equal(t, "error", string(EventAlertTypeError))
	assert.Equal(t, "warning", string(EventAlertTypeWarning))
	assert.Equal(t, "info", string(EventAlertTypeInfo))
	assert.Equal(t, "success", string(EventAlertTypeSuccess))
}
