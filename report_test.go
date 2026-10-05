package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// funcMetrics is the example from the segmentio/stats README.
type funcMetrics struct {
	calls struct {
		count int           `metric:"count" type:"counter"`
		time  time.Duration `metric:"time" type:"histogram"`
	} `metric:"func.calls"`
}

// namedMetrics returns every captured metric called name.
func namedMetrics(exp *statstest.Exporter, name string) []models.Metric {
	var out []models.Metric
	all := exp.Metrics()
	for i := range all {
		if all[i].Name == name {
			out = append(out, all[i])
		}
	}
	return out
}

func attrValue(m *models.Metric, key string) (string, bool) {
	for _, kv := range m.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func TestReportNestedNamesAndTypes(t *testing.T) {
	client, exp := statstest.NewClient(t)

	var m funcMetrics
	m.calls.count = 1
	m.calls.time = 1500 * time.Millisecond
	require.NoError(t, stats.Report(context.Background(), client, m))
	statstest.Flush(t, client)

	count := captured(t, exp, "func.calls.count")
	require.Equal(t, models.MetricTypeCounter, count.Type)
	require.InDelta(t, 1.0, count.Value, 0)

	dur := captured(t, exp, "func.calls.time")
	require.Equal(t, models.MetricTypeHistogram, dur.Type)
	require.InDelta(t, 1.5, dur.Value, 1e-12)
}

func TestReportValueKinds(t *testing.T) {
	type kinds struct {
		B   bool    `metric:"b" type:"gauge"`
		BF  bool    `metric:"bf" type:"gauge"`
		I8  int8    `metric:"i8"`
		I16 int16   `metric:"i16"`
		I32 int32   `metric:"i32"`
		I64 int64   `metric:"i64"`
		I   int     `metric:"i"`
		U8  uint8   `metric:"u8"`
		U16 uint16  `metric:"u16"`
		U32 uint32  `metric:"u32"`
		U64 uint64  `metric:"u64"`
		U   uint    `metric:"u"`
		UP  uintptr `metric:"up"`
		F32 float32 `metric:"f32"`
		F64 float64 `metric:"f64" type:"gauge"`
		Skp int     // no tags: ignored
		Dsh int     `metric:"-"`
	}
	client, exp := statstest.NewClient(t)

	v := kinds{B: true, I8: -1, I16: 2, I32: 3, I64: 4, I: 5, U8: 6, U16: 7, U32: 8, U64: 9, U: 10, UP: 11, F32: 0.5, F64: 2.25, Skp: 99, Dsh: 98}
	require.NoError(t, stats.Report(context.Background(), client, &v))
	statstest.Flush(t, client)

	want := map[string]float64{
		"b": 1, "bf": 0, "i8": -1, "i16": 2, "i32": 3, "i64": 4, "i": 5,
		"u8": 6, "u16": 7, "u32": 8, "u64": 9, "u": 10, "up": 11, "f32": 0.5, "f64": 2.25,
	}
	for name, val := range want {
		m := captured(t, exp, name)
		require.InDelta(t, val, m.Value, 0, name)
	}
	require.Empty(t, namedMetrics(exp, "Skp"))
	require.Empty(t, namedMetrics(exp, "Dsh"))
	require.Equal(t, models.MetricTypeGauge, captured(t, exp, "b").Type)
}

func TestReportTagsInherit(t *testing.T) {
	type inner struct {
		Region string `tag:"region"`
		Hits   int    `metric:"hits" type:"counter"`
	}
	type outer struct {
		Service string `tag:"service"`
		Region  string `tag:"region"`
		Env     string `tag:"env"`
		Reqs    int    `metric:"reqs" type:"counter"`
		In      inner  `metric:"inner"`
		PIn     *inner `metric:"pinner"`
		Nil     *inner `metric:"nilinner"`
	}
	client, exp := statstest.NewClient(t)

	v := outer{
		Service: "api", Region: "eu", Env: "",
		Reqs: 1,
		In:   inner{Region: "us", Hits: 2},
		PIn:  &inner{Hits: 3}, // empty override removes the inherited tag
	}
	require.NoError(t, stats.Report(context.Background(), client, v, stats.WithAttribute("extra", "x")))
	statstest.Flush(t, client)

	reqs := captured(t, exp, "reqs")
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("service", "api"), attribute.String("region", "eu"), attribute.String("extra", "x"),
	}, reqs.Attributes)

	hits := captured(t, exp, "inner.hits")
	region, _ := attrValue(&hits, "region")
	require.Equal(t, "us", region, "nested tag overrides")
	service, _ := attrValue(&hits, "service")
	require.Equal(t, "api", service, "tag flows down")
	_, hasEnv := attrValue(&hits, "env")
	require.False(t, hasEnv, "empty tag omitted")

	phits := captured(t, exp, "pinner.hits")
	_, hasRegion := attrValue(&phits, "region")
	require.False(t, hasRegion, "empty nested tag overrides and is omitted")
	service, _ = attrValue(&phits, "service")
	require.Equal(t, "api", service)

	require.Empty(t, namedMetrics(exp, "nilinner.hits"))
}

func TestReportSlice(t *testing.T) {
	type req struct {
		Route string  `tag:"route"`
		Secs  float64 `metric:"req.secs"`
	}
	client, exp := statstest.NewClient(t)

	a, b := req{"/a", 1}, req{"/b", 2}
	require.NoError(t, stats.Report(context.Background(), client, []req{a, b}))
	require.NoError(t, stats.Report(context.Background(), client, [1]*req{&b}))
	require.NoError(t, stats.Report(context.Background(), client, []*req{&a, nil}))
	require.NoError(t, stats.Report(context.Background(), client, []any{a, &b, nil}))
	statstest.Flush(t, client)

	got := namedMetrics(exp, "req.secs")
	require.Len(t, got, 2+1+1+2)
	routes := map[string]int{}
	for i := range got {
		r, _ := attrValue(&got[i], "route")
		routes[r]++
	}
	require.Equal(t, map[string]int{"/a": 3, "/b": 3}, routes)
}

func TestReportUnsupportedField(t *testing.T) {
	type bad struct {
		Ok int      `metric:"ok"`
		Ch chan int `metric:"ch"`
	}
	client, exp := statstest.NewClient(t)

	var err error
	require.NotPanics(t, func() { err = stats.Report(context.Background(), client, bad{Ok: 1}) })
	require.ErrorIs(t, err, stats.ErrUnsupportedReportField)
	// Cached error is returned again.
	require.ErrorIs(t, stats.Report(context.Background(), client, &bad{}), stats.ErrUnsupportedReportField)
	statstest.Flush(t, client)
	require.Empty(t, namedMetrics(exp, "ok"), "nothing recorded from an unsupported struct")

	type other struct {
		S  string    `metric:"s"`
		T  time.Time `metric:"t"`
		M  map[string]int
		TT int `tag:"x"`
	}
	for _, v := range []any{
		struct {
			S string `metric:"s"`
		}{},
		struct {
			T time.Time `metric:"t"`
		}{},
		struct {
			P *int `metric:"p"`
		}{},
		struct {
			N int `tag:"n"`
		}{},
		struct {
			N int `metric:"n" type:"bogus"`
		}{},
		struct {
			N string `metric:"n" tag:"n"`
		}{},
		other{},
		42,
	} {
		require.ErrorIs(t, stats.Report(context.Background(), client, v), stats.ErrUnsupportedReportField, "%T", v)
	}
}

type reportNode struct {
	V    int         `metric:"v"`
	Next *reportNode `metric:"next"`
}

func TestReportRecursiveType(t *testing.T) {
	client, _ := statstest.NewClient(t)
	require.ErrorIs(t, stats.Report(context.Background(), client, &reportNode{V: 1}), stats.ErrUnsupportedReportField)
}

func TestReportNilAndEmpty(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ctx := context.Background()

	var np *funcMetrics
	var nilIface any
	require.NoError(t, stats.Report(ctx, client, nil))
	require.NoError(t, stats.Report(ctx, client, np))
	require.NoError(t, stats.Report(ctx, client, nilIface))
	require.NoError(t, stats.Report(ctx, client, []funcMetrics(nil)))
	require.NoError(t, stats.Report(ctx, client, struct{ X int }{1}))
	require.ErrorIs(t, stats.Report(ctx, nil, funcMetrics{}), stats.ErrInvalidConfig)

	statstest.Flush(t, client)
	require.Empty(t, namedMetrics(exp, "func.calls.count"))
}

func TestReportPrefixAndContextTags(t *testing.T) {
	root, exp := statstest.NewClient(t)
	view := root.WithPrefix("svc", stats.WithAttribute("env", "test"))

	var m funcMetrics
	m.calls.count = 1
	require.NoError(t, stats.Report(context.Background(), view, m))
	statstest.Flush(t, root)

	got := captured(t, exp, "svc.func.calls.count")
	env, _ := attrValue(&got, "env")
	require.Equal(t, "test", env)
}

func TestReportAt(t *testing.T) {
	client, exp := statstest.NewClient(t)
	ts := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	var m funcMetrics
	m.calls.count = 1
	require.NoError(t, stats.ReportAt(context.Background(), client, ts, m, stats.WithTimestamp(time.Unix(1, 0))))
	statstest.Flush(t, client)

	require.True(t, captured(t, exp, "func.calls.count").Timestamp.Equal(ts))
}

func TestReportJoinsRecordErrors(t *testing.T) {
	type v struct {
		A float64 `metric:"a"`
		B float64 `metric:"b"`
	}
	client, _ := statstest.NewClient(t)
	nan := v{A: nanValue(), B: nanValue()}
	err := stats.Report(context.Background(), client, nan)
	require.Error(t, err)
	require.NotErrorIs(t, err, stats.ErrUnsupportedReportField)
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}

func BenchmarkReport(b *testing.B) {
	type bench struct {
		Service string  `tag:"service"`
		Reqs    int     `metric:"reqs" type:"counter"`
		Secs    float64 `metric:"secs"`
		Calls   struct {
			Count int           `metric:"count" type:"counter"`
			Time  time.Duration `metric:"time"`
		} `metric:"calls"`
	}
	v := bench{Service: "api", Reqs: 1, Secs: 0.1}
	v.Calls.Count = 1
	v.Calls.Time = time.Millisecond

	client := stats.NewNoOpClient()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := stats.Report(ctx, client, &v); err != nil {
			b.Fatal(err)
		}
	}
}
