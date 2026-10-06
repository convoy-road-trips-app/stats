package stats

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/exporters"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestClientFlushWritesThroughBufferExporter(t *testing.T) {
	// Given: a client whose custom exporter is a Buffer with a large target size
	dst := &lockedBuffer{}
	client, err := NewClient(
		WithVersionReporting(false),
		WithFlushInterval(time.Hour),
		WithExporter(&exporters.Buffer{Dst: dst, Serializer: exporters.NewLineSerializer()}),
	)
	require.NoError(t, err)
	defer client.Close()

	// When
	require.NoError(t, client.Counter(context.Background(), "buffered.hits", 1))
	require.NoError(t, client.Flush(context.Background()))

	// Then: Flush returned with the data already in the destination
	require.True(t, strings.Contains(dst.String(), "buffered.hits"), "destination: %q", dst.String())
}
