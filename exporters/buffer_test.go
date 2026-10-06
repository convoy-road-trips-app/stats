package exporters

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats/models"
)

type syncBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes int
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func counter(name string, v float64) *models.Metric {
	return &models.Metric{Name: name, Type: models.MetricTypeCounter, Value: v}
}

func TestBufferWritesWhenTargetSizeReached(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer(), BufferSize: 10, BufferPoolSize: 1}

	require.NoError(t, b.Handle(counter("a", 1)))
	require.Empty(t, dst.String(), "below the target size nothing is written")

	require.NoError(t, b.Handle(counter("b", 2)))
	require.Equal(t, "a:1|c\nb:2|c\n", dst.String())
	require.Equal(t, 1, dst.writes)
}

func TestBufferFlushWritesRemainder(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer()}

	require.NoError(t, b.Handle(counter("a", 1), counter("b", 2)))
	require.Empty(t, dst.String())
	require.NoError(t, b.Flush())
	require.Equal(t, "a:1|c\nb:2|c\n", dst.String())

	writes := dst.writes
	require.NoError(t, b.Flush())
	require.Equal(t, writes, dst.writes, "an empty flush writes nothing")
}

func TestBufferOversizedMetricStillWritten(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer(), BufferSize: 1}
	require.NoError(t, b.Handle(counter("long.metric.name", 1)))
	require.Equal(t, "long.metric.name:1|c\n", dst.String())
}

func TestBufferAsExporter(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer()}
	var e models.Exporter = b
	require.Equal(t, "buffer", e.Name())
	require.NoError(t, e.Export(context.Background(), []*models.Metric{counter("a", 1)}))
	require.NoError(t, e.Shutdown(context.Background()))
	require.Equal(t, "a:1|c\n", dst.String())
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestBufferReportsWriteAndConfigErrors(t *testing.T) {
	boom := errors.New("boom")
	b := &Buffer{Dst: failWriter{boom}, Serializer: NewLineSerializer(), BufferSize: 1}
	require.ErrorIs(t, b.Handle(counter("a", 1)), boom)
	require.NoError(t, b.Handle(), "no metrics, no work")

	require.ErrorIs(t, (&Buffer{Serializer: NewLineSerializer()}).Handle(counter("a", 1)), errBufferConfig)
	require.ErrorIs(t, (&Buffer{Dst: &syncBuffer{}}).Handle(counter("a", 1)), errBufferConfig)
	require.ErrorIs(t, (&Buffer{}).Flush(), errBufferConfig)
}

func TestBufferConcurrentNoLoss(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer(), BufferSize: 64, BufferPoolSize: 2}

	const workers, each = 16, 200
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				herr := b.Handle(counter("m", 1))
				if herr != nil {
					t.Error(herr)
				}
			}
		}()
	}
	wg.Wait()
	require.NoError(t, b.Flush())

	lines := strings.Split(strings.TrimSuffix(dst.String(), "\n"), "\n")
	require.Len(t, lines, workers*each)
	for _, l := range lines {
		require.Equal(t, "m:1|c", l)
	}
}

func TestBufferExportWritesThrough(t *testing.T) {
	dst := &syncBuffer{}
	b := &Buffer{Dst: dst, Serializer: NewLineSerializer()}

	require.NoError(t, b.Handle(counter("a", 1)))
	require.Empty(t, dst.String(), "Handle batches below the target size")

	require.NoError(t, b.Export(context.Background(), []*models.Metric{counter("b", 2)}))
	require.Equal(t, "a:1|c\nb:2|c\n", dst.String(), "Export writes accepted data, including earlier Handle data")
}
