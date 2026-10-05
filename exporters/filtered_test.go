package exporters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/models"
)

type plainExp struct {
	name        string
	exported    [][]*models.Metric
	exportErr   error
	shutdownErr error
	shutdowns   int
}

func (p *plainExp) Name() string { return p.name }
func (p *plainExp) Export(_ context.Context, m []*models.Metric) error {
	p.exported = append(p.exported, m)
	return p.exportErr
}
func (p *plainExp) Shutdown(context.Context) error { p.shutdowns++; return p.shutdownErr }

type boundedExp struct {
	*plainExp
	d time.Duration
}

func (b boundedExp) ExportTimeout() time.Duration { return b.d }

type idleExp struct {
	*plainExp
	idles int
	err   error
}

func (i *idleExp) ExportIdle(context.Context) error { i.idles++; return i.err }

type bothExp struct {
	*plainExp
	d     time.Duration
	idles int
}

func (b *bothExp) ExportTimeout() time.Duration     { return b.d }
func (b *bothExp) ExportIdle(context.Context) error { b.idles++; return nil }

func batch(names ...string) []*models.Metric {
	out := make([]*models.Metric, 0, len(names))
	for _, n := range names {
		out = append(out, &models.Metric{Name: n})
	}
	return out
}

func keepKept(in []*models.Metric) []*models.Metric {
	var out []*models.Metric
	for _, m := range in {
		if m.Name == "keep" {
			out = append(out, m)
		}
	}
	return out
}

func caps(e models.Exporter) (timeout, idle bool) {
	_, timeout = e.(models.ExportTimeouter)
	_, idle = e.(models.IdleExporter)
	return
}

func TestFilteredForwardsExportTimeout(t *testing.T) {
	inner := boundedExp{plainExp: &plainExp{name: "b"}, d: 500 * time.Millisecond}
	f := exporters.Filtered(inner, keepKept)
	et, ok := f.(models.ExportTimeouter)
	if !ok {
		t.Fatal("wrapper must implement ExportTimeouter")
	}
	if got := et.ExportTimeout(); got != 500*time.Millisecond {
		t.Fatalf("ExportTimeout = %v, want 500ms", got)
	}
}

func TestFilteredCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		inner       models.Exporter
		wantTimeout bool
		wantIdle    bool
	}{
		{"plain", &plainExp{name: "p"}, false, false},
		{"bounded", boundedExp{plainExp: &plainExp{name: "p"}, d: time.Second}, true, false},
		{"idle", &idleExp{plainExp: &plainExp{name: "p"}}, false, true},
		{"both", &bothExp{plainExp: &plainExp{name: "p"}, d: time.Second}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := exporters.Filtered(tt.inner, keepKept)
			gotT, gotI := caps(f)
			if gotT != tt.wantTimeout || gotI != tt.wantIdle {
				t.Fatalf("caps = (timeout=%v idle=%v), want (%v %v)", gotT, gotI, tt.wantTimeout, tt.wantIdle)
			}
			if f.Name() != "p" {
				t.Fatalf("Name = %q, want p", f.Name())
			}
		})
	}
}

func TestFilteredAppliesFilter(t *testing.T) {
	inner := &plainExp{name: "p"}
	f := exporters.Filtered(inner, keepKept)
	in := batch("drop", "keep", "drop", "keep")
	if err := f.Export(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(inner.exported) != 1 || len(inner.exported[0]) != 2 {
		t.Fatalf("inner got %v, want one batch of 2", inner.exported)
	}
	for _, m := range inner.exported[0] {
		if m.Name != "keep" {
			t.Fatalf("unexpected metric %q", m.Name)
		}
	}
	if len(in) != 4 {
		t.Fatal("input batch was modified")
	}
}

func TestFilteredEmptyBatchSkipsExport(t *testing.T) {
	inner := &plainExp{name: "p", exportErr: errors.New("should not be returned")}
	f := exporters.Filtered(inner, keepKept)
	if err := f.Export(context.Background(), batch("drop")); err != nil {
		t.Fatalf("empty filtered batch must return nil, got %v", err)
	}
	if len(inner.exported) != 0 {
		t.Fatalf("inner Export called %d times for empty batch", len(inner.exported))
	}
}

func TestFilteredNilFilterPassesThrough(t *testing.T) {
	inner := &plainExp{name: "p"}
	f := exporters.Filtered(inner, nil)
	if err := f.Export(context.Background(), batch("a", "b")); err != nil {
		t.Fatal(err)
	}
	if len(inner.exported) != 1 || len(inner.exported[0]) != 2 {
		t.Fatalf("inner got %v", inner.exported)
	}
}

func TestFilteredPropagatesExportError(t *testing.T) {
	want := errors.New("boom")
	inner := &plainExp{name: "p", exportErr: want}
	f := exporters.Filtered(inner, keepKept)
	if err := f.Export(context.Background(), batch("keep")); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestFilteredForwardsShutdown(t *testing.T) {
	want := errors.New("shutdown failed")
	inners := []*plainExp{{name: "a", shutdownErr: want}, {name: "b", shutdownErr: want}, {name: "c", shutdownErr: want}, {name: "d", shutdownErr: want}}
	wrapped := []models.Exporter{
		exporters.Filtered(inners[0], keepKept),
		exporters.Filtered(boundedExp{plainExp: inners[1], d: time.Second}, keepKept),
		exporters.Filtered(&idleExp{plainExp: inners[2]}, keepKept),
		exporters.Filtered(&bothExp{plainExp: inners[3], d: time.Second}, keepKept),
	}
	for i, w := range wrapped {
		if err := w.Shutdown(context.Background()); !errors.Is(err, want) {
			t.Fatalf("wrapper %d: err = %v, want %v", i, err, want)
		}
		if inners[i].shutdowns != 1 {
			t.Fatalf("wrapper %d: shutdowns = %d, want 1", i, inners[i].shutdowns)
		}
	}
}

func TestFilteredForwardsExportIdle(t *testing.T) {
	want := errors.New("idle failed")
	idle := &idleExp{plainExp: &plainExp{name: "i"}, err: want}
	f := exporters.Filtered(idle, keepKept)
	if err := f.(models.IdleExporter).ExportIdle(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if idle.idles != 1 {
		t.Fatalf("idles = %d, want 1", idle.idles)
	}

	both := &bothExp{plainExp: &plainExp{name: "b"}, d: 3 * time.Second}
	fb := exporters.Filtered(both, keepKept)
	if err := fb.(models.IdleExporter).ExportIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if both.idles != 1 {
		t.Fatalf("both.idles = %d, want 1", both.idles)
	}
	if fb.(models.ExportTimeouter).ExportTimeout() != 3*time.Second {
		t.Fatal("both wrapper must forward ExportTimeout")
	}
}
