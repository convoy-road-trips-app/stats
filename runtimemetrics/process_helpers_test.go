package runtimemetrics

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

type fakeFS struct {
	mu    sync.Mutex
	files map[string][]byte
	errs  map[string]error
	fds   int
}

func (f *fakeFS) source() *processSource {
	return &processSource{
		readFile: func(path string) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := f.errs[path]; err != nil {
				return nil, err
			}
			b, ok := f.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return b, nil
		},
		countDir: func(path string) (int, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := f.errs[path]; err != nil {
				return 0, err
			}
			return f.fds, nil
		},
	}
}

func newFakeFS(t *testing.T) *fakeFS {
	t.Helper()
	td := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(t, err)
		return b
	}
	return &fakeFS{
		files: map[string][]byte{
			procStatPath:    td("proc_stat_parens.txt"),
			procStatusPath:  td("proc_status.txt"),
			procLimitsPath:  td("proc_limits.txt"),
			procMeminfoPath: td("proc_meminfo.txt"),
			cgroupMemoryMax: td("cgroup_memory_max_max.txt"),
		},
		errs: map[string]error{},
		fds:  17,
	}
}

type obs struct {
	name  string
	mtype models.MetricType
	value float64
	attrs []attribute.KeyValue
}

type recorder struct {
	mu   sync.Mutex
	list []obs
}

func (r *recorder) record(name string, mtype models.MetricType, value float64, attrs ...attribute.KeyValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, obs{name, mtype, value, attrs})
}

// get returns the value for name with the given "type" attribute value ("" for none).
func (r *recorder) get(name, typ string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.list {
		if o.name != name {
			continue
		}
		var got string
		for _, a := range o.attrs {
			if a.Key == "type" {
				got = a.Value.AsString()
			}
		}
		if got == typ {
			return o.value, true
		}
	}
	return 0, false
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = nil
}

func (r *recorder) hasPrefix(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.list {
		if len(o.name) >= len(prefix) && o.name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

type errLog struct {
	mu    sync.Mutex
	calls []string
}

func (e *errLog) onError(source string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, fmt.Sprintf("%s: %v", source, err))
}

func newProcessCollector(t *testing.T, fs *fakeFS, rec *recorder, el *errLog) *Collector {
	t.Helper()
	cfg := Config{Prefix: "runtime.go", ProcessMetrics: true}
	if el != nil {
		cfg.OnError = el.onError
	}
	c := New(cfg, rec.record)
	c.proc = newProcessState(fs.source())
	return c
}
