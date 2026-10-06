// Package debugstats provides an exporter that prints every metric to a writer
// as one StatsD-format line, for troubleshooting what an application emits.
package debugstats

import (
	"context"
	"io"
	"os"
	"regexp"
	"sync"

	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/models"
)

// serializer renders `name:value|type|#k:v,...` lines. It holds no per-call
// state, so one instance is shared.
var serializer = exporters.NewLineSerializer()

// Exporter writes one StatsD-format line per metric, such as
// `server.start:1|c` or `http.requests:1|c|#method:GET`. It is safe for
// concurrent use; lines from concurrent exports never interleave. The zero
// value writes to os.Stdout.
type Exporter struct {
	// Dst receives the lines. Nil means os.Stdout.
	Dst io.Writer
	// Grep, when set, restricts output to lines it matches.
	Grep *regexp.Regexp

	mu sync.Mutex
}

var (
	_ models.Exporter = (*Exporter)(nil)
	_ io.Writer      = (*Exporter)(nil)
)

// Name returns the exporter name.
func (e *Exporter) Name() string { return "debugstats" }

// Export writes each metric, or only those whose line matches Grep, as a
// newline-terminated line. The metrics are not modified.
func (e *Exporter) Export(_ context.Context, metrics []*models.Metric) error {
	packets, err := serializer.Serialize(metrics)
	if err != nil {
		return err
	}

	var out []byte
	for _, p := range packets {
		if e.Grep != nil && !e.Grep.Match(p) {
			continue
		}
		out = append(out, p...)
		out = append(out, '\n')
	}
	if len(out) == 0 {
		return nil
	}

	_, err = e.Write(out)
	return err
}

// Write writes p to Dst, or to os.Stdout when Dst is nil, without any
// serialization, so a caller can interleave its own text with the exported
// lines. It is serialized with Export, so concurrent writes never interleave.
func (e *Exporter) Write(p []byte) (int, error) {
	dst := e.Dst
	if dst == nil {
		dst = os.Stdout
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	return dst.Write(p)
}

// Shutdown is a no-op; the exporter holds no resources and never closes Dst.
func (e *Exporter) Shutdown(context.Context) error { return nil }
