package serializers

import (
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func serializeEvent(s *DogStatsDSerializer, e *models.DatadogEvent) string {
	return string(s.SerializeEvent(e))
}

func TestEventSerializeEscapesNewline(t *testing.T) {
	got := serializeEvent(NewDogStatsDSerializer(nil), &models.DatadogEvent{Title: "t", Text: "a\nb"})
	assert.Equal(t, `_e{1,4}:t|a\nb`, got)
}

func TestEventSerializeEscapesNewlineInTitle(t *testing.T) {
	got := serializeEvent(NewDogStatsDSerializer(nil), &models.DatadogEvent{Title: "x\ny", Text: "z"})
	assert.Equal(t, `_e{4,1}:x\ny|z`, got)
}

func TestEventSerializeUnicodeByteLength(t *testing.T) {
	// "é" is 2 bytes, "日本" is 6 bytes.
	got := serializeEvent(NewDogStatsDSerializer(nil), &models.DatadogEvent{Title: "é", Text: "日本"})
	assert.Equal(t, "_e{2,6}:é|日本", got)
}

func TestEventSerializeEmptyOptionalsOmitted(t *testing.T) {
	got := serializeEvent(NewDogStatsDSerializer(nil), &models.DatadogEvent{Title: "t", Text: "x"})
	assert.Equal(t, "_e{1,1}:t|x", got)
}

func TestEventSerializeAllFields(t *testing.T) {
	e := &models.DatadogEvent{
		Title:          "deploy",
		Text:           "done",
		Timestamp:      time.Unix(1700000000, 123),
		Host:           "web-1",
		Priority:       models.EventPriorityLow,
		AlertType:      models.EventAlertTypeSuccess,
		AggregationKey: "agg",
		SourceTypeName: "ci",
		Tags:           []attribute.KeyValue{attribute.String("env", "prod"), attribute.Int("n", 2)},
	}
	assert.Equal(t,
		"_e{6,4}:deploy|done|d:1700000000|h:web-1|p:low|t:success|k:agg|s:ci|#env:prod,n:2",
		serializeEvent(NewDogStatsDSerializer(nil), e))
}

func TestEventSerializePriorityAndAlertOmittedWhenEmpty(t *testing.T) {
	e := &models.DatadogEvent{Title: "t", Text: "x", Host: "h", AggregationKey: "k"}
	got := serializeEvent(NewDogStatsDSerializer(nil), e)
	assert.Equal(t, "_e{1,1}:t|x|h:h|k:k", got)
	assert.NotContains(t, got, "|p:")
	assert.NotContains(t, got, "|t:")
}

func TestEventSerializePriorityAndAlertPresent(t *testing.T) {
	e := &models.DatadogEvent{Title: "t", Text: "x", Priority: models.EventPriorityNormal, AlertType: models.EventAlertTypeError}
	assert.Equal(t, "_e{1,1}:t|x|p:normal|t:error", serializeEvent(NewDogStatsDSerializer(nil), e))
}

func TestEventSerializeTagFilters(t *testing.T) {
	s := NewDogStatsDSerializer([]string{"env:prod", "http_req_path:/g"}, WithTagFilters([]string{"http_req_path", "secret"}))
	e := &models.DatadogEvent{Title: "t", Text: "x", Tags: []attribute.KeyValue{
		attribute.String("secret", "s"), attribute.String("keep", "v"), attribute.String("http_req_path", "/a"),
	}}
	assert.Equal(t, "_e{1,1}:t|x|#env:prod,keep:v", serializeEvent(s, e))
}

func TestEventSerializeAllTagsFilteredNoTagSection(t *testing.T) {
	s := NewDogStatsDSerializer(nil, WithTagFilters([]string{"a"}))
	e := &models.DatadogEvent{Title: "t", Text: "x", Tags: []attribute.KeyValue{attribute.String("a", "1")}}
	assert.Equal(t, "_e{1,1}:t|x", serializeEvent(s, e))
}

func TestEventSerializeDoesNotAliasPooledBuffer(t *testing.T) {
	s := NewDogStatsDSerializer(nil)
	first := s.SerializeEvent(&models.DatadogEvent{Title: "aaaa", Text: "bbbb"})
	_ = s.SerializeEvent(&models.DatadogEvent{Title: "cccc", Text: "dddd"})
	assert.Equal(t, "_e{4,4}:aaaa|bbbb", string(first))
}
