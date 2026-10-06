package statstest

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

func TestDogStatsDMetricString(t *testing.T) {
	tests := []struct {
		name string
		m    DogStatsDMetric
		want string
	}{
		{"plain", DogStatsDMetric{Name: "a.b", Value: 1, Type: DogStatsDCounter, SampleRate: 1}, "a.b:1|c"},
		{"unset rate", DogStatsDMetric{Name: "a", Value: 2.5, Type: DogStatsDGauge}, "a:2.5|g"},
		{"rate and tags", DogStatsDMetric{Name: "a", Value: 3, Type: DogStatsDHistogram, SampleRate: 0.5, Tags: []string{"x:1", "y:2"}}, "a:3|h|@0.5|#x:1,y:2"},
		{"timing", DogStatsDMetric{Name: "t", Value: 120, Type: DogStatsDTiming, SampleRate: 1}, "t:120|ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.m.String())
			require.Equal(t, tt.want, sprint(tt.m))
			require.Equal(t, tt.want, sprint(&tt.m))
			got, ok := parseDogStatsDMetric(tt.want)
			require.True(t, ok)
			require.Equal(t, tt.want, got.String(), "round trip")
		})
	}
}

func TestDogStatsDEventString(t *testing.T) {
	e := DogStatsDEvent{
		Title:          "deploy",
		Text:           "line1\nline2",
		Timestamp:      time.Unix(1700000000, 0),
		Host:           "h1",
		Priority:       models.EventPriority("low"),
		AlertType:      models.EventAlertType("info"),
		AggregationKey: "k",
		SourceTypeName: "src",
		Tags:           []string{"env:prod"},
	}
	want := `_e{6,12}:deploy|line1\nline2|d:1700000000|h:h1|p:low|t:info|k:k|s:src|#env:prod`
	require.Equal(t, want, e.String())
	require.Equal(t, want, sprint(e))

	got, ok := parseDogStatsDEvent(want)
	require.True(t, ok)
	require.Equal(t, e.Text, got.Text)
	require.Equal(t, want, got.String(), "round trip")

	require.Equal(t, "_e{1,0}:t|", DogStatsDEvent{Title: "t"}.String())
}

// sprint formats v through fmt, so the fmt.Formatter path is what is tested.
func sprint(v any) string { return fmt.Sprint(v) }
