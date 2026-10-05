package statstest

import (
	"context"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

// flushTimeout bounds Flush so a stuck pipeline fails the test instead of
// hanging it.
const flushTimeout = 5 * time.Second

// NewClient returns a stats.Client that exports to the returned Exporter, in
// addition to any backends opts enable. The client is registered with
// stats.WithExporter after opts, and closed in t.Cleanup.
//
// Metrics reach the Exporter asynchronously. Call Flush before asserting, so
// the result does not depend on timing.
//
// The capture may contain metrics the client records itself, not only the ones
// the test recorded. Assert on metrics by name rather than on the full list.
func NewClient(t testing.TB, opts ...stats.Option) (*stats.Client, *Exporter) {
	t.Helper()

	exp := NewExporter()
	all := make([]stats.Option, 0, len(opts)+1)
	all = append(all, opts...)
	all = append(all, stats.WithExporter(exp))

	client, err := stats.NewClient(all...)
	if err != nil {
		t.Fatalf("statstest: create client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("statstest: close client: %v", err)
		}
	})
	return client, exp
}

// Flush exports everything client has accepted so far and fails the test if
// that does not finish within five seconds or an exporter reports an error.
// Afterwards Exporter.Metrics holds those observations.
func Flush(t testing.TB, client *stats.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if err := client.Flush(ctx); err != nil {
		t.Fatalf("statstest: flush: %v", err)
	}
}
