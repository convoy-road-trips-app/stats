package exporters

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

type fakeExporter struct {
	name     string
	sleep    time.Duration
	err      error
	panicVal any
	exports  atomic.Int32
	shutdown atomic.Int32
}

func (f *fakeExporter) Name() string { return f.name }

func (f *fakeExporter) Export(ctx context.Context, _ []*models.Metric) error {
	f.exports.Add(1)
	if f.panicVal != nil {
		panic(f.panicVal)
	}
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func (f *fakeExporter) Shutdown(context.Context) error {
	f.shutdown.Add(1)
	return f.err
}

type timeoutExporter struct {
	*fakeExporter
	timeout time.Duration
}

func (t *timeoutExporter) ExportTimeout() time.Duration { return t.timeout }

type idleExporter struct {
	*fakeExporter
	idle atomic.Int32
}

func (i *idleExporter) ExportIdle(ctx context.Context) error {
	i.idle.Add(1)
	return i.err
}

func TestMultiFansOut(t *testing.T) {
	a, b := &fakeExporter{name: "a"}, &fakeExporter{name: "b"}
	m := Multi("multi", time.Second, a, nil, b)
	if m.Name() != "multi" {
		t.Fatalf("name = %q", m.Name())
	}
	if err := m.Export(context.Background(), []*models.Metric{{}}); err != nil {
		t.Fatal(err)
	}
	if a.exports.Load() != 1 || b.exports.Load() != 1 {
		t.Fatalf("exports a=%d b=%d", a.exports.Load(), b.exports.Load())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.shutdown.Load() != 1 || b.shutdown.Load() != 1 {
		t.Fatal("shutdown not fanned out")
	}
}

func TestMultiPanickingChildDoesNotStopOthers(t *testing.T) {
	bad := &fakeExporter{name: "bad", panicVal: "boom"}
	good := &fakeExporter{name: "good"}
	m := Multi("multi", time.Second, bad, good)
	err := m.Export(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "bad: panic: boom") {
		t.Fatalf("err = %v", err)
	}
	if good.exports.Load() != 1 {
		t.Fatal("good child did not run")
	}
}

func TestMultiIdleForwardingSkipsChildrenWithoutIt(t *testing.T) {
	plain := &fakeExporter{name: "plain"}
	idle := &idleExporter{fakeExporter: &fakeExporter{name: "idle"}}
	m := Multi("multi", time.Second, plain, idle)
	ie, ok := m.(models.IdleExporter)
	if !ok {
		t.Fatal("Multi must always implement IdleExporter")
	}
	if err := ie.ExportIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if idle.idle.Load() != 1 {
		t.Fatalf("idle calls = %d", idle.idle.Load())
	}
	if plain.exports.Load() != 0 {
		t.Fatal("plain child must not be touched")
	}
	// No idle-capable children: no-op.
	if err := Multi("m", time.Second, plain).(models.IdleExporter).ExportIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMultiJoinsErrors(t *testing.T) {
	e1, e2 := errors.New("e1"), errors.New("e2")
	m := Multi("multi", time.Second,
		&fakeExporter{name: "a", err: e1},
		&fakeExporter{name: "ok"},
		&fakeExporter{name: "b", err: e2})
	err := m.Export(context.Background(), nil)
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("err = %v", err)
	}
	err = m.Shutdown(context.Background())
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("shutdown err = %v", err)
	}
}

func TestMultiExportTimeout(t *testing.T) {
	plain := &fakeExporter{name: "plain"}
	long := &timeoutExporter{fakeExporter: &fakeExporter{name: "long"}, timeout: 500 * time.Millisecond}
	short := &timeoutExporter{fakeExporter: &fakeExporter{name: "short"}, timeout: 10 * time.Millisecond}

	get := func(m models.Exporter) time.Duration {
		te, ok := m.(models.ExportTimeouter)
		if !ok {
			t.Fatal("Multi must always implement ExportTimeouter")
		}
		return te.ExportTimeout()
	}
	if d := get(Multi("m", 50*time.Millisecond, plain, long, short)); d != 500*time.Millisecond {
		t.Fatalf("timeout = %v, want 500ms", d)
	}
	if d := get(Multi("m", 50*time.Millisecond, plain, short)); d != 50*time.Millisecond {
		t.Fatalf("timeout = %v, want default 50ms", d)
	}
}

func TestMultiBoundedChildOutlivesDefault(t *testing.T) {
	bounded := &timeoutExporter{
		fakeExporter: &fakeExporter{name: "bounded", sleep: 200 * time.Millisecond},
		timeout:      500 * time.Millisecond,
	}
	unbounded := &fakeExporter{name: "unbounded", sleep: 200 * time.Millisecond}
	m := Multi("multi", 50*time.Millisecond, bounded, unbounded)

	err := m.Export(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from unbounded sibling")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if !strings.HasPrefix(err.Error(), "unbounded: ") {
		t.Fatalf("wrong child blamed: %v", err)
	}
	// The bounded child must have succeeded: only one joined error.
	if n := strings.Count(err.Error(), "\n"); n != 0 {
		t.Fatalf("expected a single child error, got: %v", err)
	}
}
