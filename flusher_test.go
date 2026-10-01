package stats

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// legacyRecorder has exactly Recorder's method set, as a v1.0.1 implementer
// does. Embedding the interface, not NoOpClient, keeps Flush out of it.
type legacyRecorder struct{ Recorder }

var _ Recorder = legacyRecorder{}

func TestNoOpClient_Flush_returns_nil(t *testing.T) {
	// Given
	client := NewNoOpClient()

	// When
	err := client.Flush(context.Background())

	// Then
	require.NoError(t, err)
}

func TestFlusher_is_implemented_by_Client_and_NoOpClient(t *testing.T) {
	// Given: both recorders held as a Recorder
	client, err := NewClient(WithFlushInterval(time.Hour))
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Shutdown(context.Background())) }()
	recorders := map[string]Recorder{"Client": client, "NoOpClient": NewNoOpClient()}

	for name, recorder := range recorders {
		t.Run(name, func(t *testing.T) {
			// When: the caller asks for the optional capability
			flusher, ok := recorder.(Flusher)

			// Then
			require.True(t, ok, "%s does not implement Flusher", name)
			require.NoError(t, flusher.Flush(context.Background()))
		})
	}
}

func TestFlusher_is_optional_for_Recorder_implementations(t *testing.T) {
	// Given: a Recorder written against v1.0.1
	var recorder Recorder = legacyRecorder{Recorder: NewNoOpClient()}

	// When
	_, ok := recorder.(Flusher)

	// Then: it still satisfies Recorder, and callers see it has no Flush
	require.False(t, ok)
}
