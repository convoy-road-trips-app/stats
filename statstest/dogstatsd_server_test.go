package statstest_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/convoy-road-trips-app/stats"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/serializers"
	"github.com/convoy-road-trips-app/stats/statstest"
)

// recorder is a DogStatsDHandler that keeps what it receives.
type recorder struct {
	mu      sync.Mutex
	metrics []statstest.DogStatsDMetric
	events  []statstest.DogStatsDEvent
	froms   []net.Addr
}

func (r *recorder) HandleMetric(m statstest.DogStatsDMetric, from net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, m)
	r.froms = append(r.froms, from)
}

//nolint:gocritic // hugeParam: signature is fixed by DogStatsDHandler.
func (r *recorder) HandleEvent(e statstest.DogStatsDEvent, _ net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) Metrics() []statstest.DogStatsDMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]statstest.DogStatsDMetric(nil), r.metrics...)
}

func (r *recorder) Events() []statstest.DogStatsDEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]statstest.DogStatsDEvent(nil), r.events...)
}

func (r *recorder) metric(name string) (statstest.DogStatsDMetric, bool) {
	for _, m := range r.Metrics() {
		if m.Name == name {
			return m, true
		}
	}
	return statstest.DogStatsDMetric{}, false
}

// send writes one datagram to a UDP server address.
func send(t *testing.T, addr, payload string) {
	t.Helper()
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "udp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
}

// sentinel sends a valid line and waits for it, proving every datagram sent
// before it was already processed (UDP on loopback is ordered in practice, and
// the server handles datagrams sequentially).
func sentinel(t *testing.T, addr string, r *recorder) {
	t.Helper()
	send(t, addr, "sentinel:1|c")
	require.Eventually(t, func() bool { _, ok := r.metric("sentinel"); return ok },
		5*time.Second, 5*time.Millisecond)
}

func TestServerParsesMetricsAndEvents(t *testing.T) {
	r := &recorder{}
	addr := statstest.NewDogStatsDServer(t, r)

	client, _ := statstest.NewClient(t, stats.WithDatadog(&stats.DatadogConfig{
		Enabled:  true,
		Endpoint: addr,
	}))
	ctx := context.Background()
	require.NoError(t, client.Counter(ctx, "rt.count", 3, stats.WithAttribute("route", "/a")))
	require.NoError(t, client.Gauge(ctx, "rt.gauge", 2.5))
	require.NoError(t, client.Histogram(ctx, "rt.hist", 7))
	statstest.Flush(t, client)

	// Events are not recorded by a client, so serialize one with the same
	// serializer the Datadog exporter uses and send it raw.
	event := serializers.NewDogStatsDSerializer(nil).SerializeEvent(&models.DatadogEvent{
		Title:     "deploy",
		Text:      "line1\nline2",
		Timestamp: time.Unix(1700000000, 0),
		Host:      "h1",
		Priority:  models.EventPriorityLow,
		AlertType: models.EventAlertTypeWarning,
		Tags:      nil,
	})
	send(t, addr, string(event))

	require.Eventually(t, func() bool {
		_, c := r.metric("rt.count")
		_, g := r.metric("rt.gauge")
		_, h := r.metric("rt.hist")
		return c && g && h && len(r.Events()) == 1
	}, 5*time.Second, 5*time.Millisecond)

	c, _ := r.metric("rt.count")
	require.Equal(t, statstest.DogStatsDCounter, c.Type)
	require.InDelta(t, 3, c.Value, 1e-9)
	require.InDelta(t, 1, c.SampleRate, 1e-9)
	require.Contains(t, c.Tags, "route:/a")

	g, _ := r.metric("rt.gauge")
	require.Equal(t, statstest.DogStatsDGauge, g.Type)
	require.InDelta(t, 2.5, g.Value, 1e-9)

	h, _ := r.metric("rt.hist")
	require.Equal(t, statstest.DogStatsDHistogram, h.Type)
	require.InDelta(t, 7, h.Value, 1e-9)

	require.Equal(t, []statstest.DogStatsDEvent{{
		Title:     "deploy",
		Text:      "line1\nline2",
		Timestamp: time.Unix(1700000000, 0),
		Host:      "h1",
		Priority:  models.EventPriorityLow,
		AlertType: models.EventAlertTypeWarning,
	}}, r.Events())
	require.NotNil(t, r.froms[0])
}

func TestServerUnixgramRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unixgram is unavailable on Windows")
	}
	dir, err := os.MkdirTemp("", "sp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")

	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "unixgram", path)
	require.NoError(t, err)
	r := &recorder{}
	done := make(chan error, 1)
	go func() { done <- statstest.DogStatsDServer{}.Serve(conn, r) }()
	t.Cleanup(func() {
		_ = conn.Close()
		require.NoError(t, <-done)
	})

	client, _ := statstest.NewClient(t, stats.WithDatadog(&stats.DatadogConfig{
		Enabled:  true,
		Endpoint: "unixgram://" + path,
	}))
	require.NoError(t, client.Counter(context.Background(), "ux.count", 4))
	statstest.Flush(t, client)

	require.Eventually(t, func() bool { _, ok := r.metric("ux.count"); return ok },
		5*time.Second, 5*time.Millisecond)
	m, _ := r.metric("ux.count")
	require.Equal(t, statstest.DogStatsDCounter, m.Type)
	require.InDelta(t, 4, m.Value, 1e-9)
}

func TestServerListenAndServeAddresses(t *testing.T) {
	require.Error(t, statstest.DogStatsDServer{}.ListenAndServe("tcp://127.0.0.1:0", &recorder{}))
	require.Error(t, statstest.DogStatsDServer{}.ListenAndServe("unixgram://", &recorder{}))
	require.Error(t, statstest.ListenAndServeDogStatsD("not-an-address", &recorder{}))
}

func TestServerRejectsMalformedLine(t *testing.T) {
	r := &recorder{}
	addr := statstest.NewDogStatsDServer(t, r)

	for _, bad := range []string{
		"garbage",
		":1|c",
		"a:1",
		"a:x|c",
		"a:1|zz",
		"a:1|c|@0",
		"a:1|c|@2",
		"a:1|c|@nope",
		"_e{",
		"_e{x,y}:a|b",
		"_e{5,1}:ab|c",
		"_e{1,1}:ab|c",
		"_e{0,1}:|c",
		"_e{1,1}:a|b|d:nope",
		"_sc|name|0",
		"\x00\xff\xfe",
		"",
	} {
		send(t, addr, bad)
	}
	sentinel(t, addr, r)

	require.Len(t, r.Metrics(), 1, "only the sentinel is valid")
	require.Empty(t, r.Events())
}

func TestServerSampleRatesAndTags(t *testing.T) {
	r := &recorder{}
	addr := statstest.NewDogStatsDServer(t, r)

	send(t, addr, "a:1|c|@0.25|#env:prod,,role:web")
	send(t, addr, "b:2|ms|#only")
	send(t, addr, "c:3|d|@1")
	send(t, addr, "d:4|s")
	send(t, addr, "e:5|g|#t:1|c:container")
	sentinel(t, addr, r)

	get := func(name string) statstest.DogStatsDMetric {
		m, ok := r.metric(name)
		require.True(t, ok, name)
		return m
	}
	a := get("a")
	require.InDelta(t, 0.25, a.SampleRate, 1e-9)
	require.Equal(t, []string{"env:prod", "role:web"}, a.Tags)

	b := get("b")
	require.Equal(t, statstest.DogStatsDTiming, b.Type)
	require.InDelta(t, 1, b.SampleRate, 1e-9)
	require.Equal(t, []string{"only"}, b.Tags)

	require.Equal(t, statstest.DogStatsDDistribution, get("c").Type)
	d := get("d")
	require.Equal(t, statstest.DogStatsDSet, d.Type)
	require.Nil(t, d.Tags)
	require.Equal(t, []string{"t:1"}, get("e").Tags)
}

func TestServerMultiLineDatagram(t *testing.T) {
	r := &recorder{}
	addr := statstest.NewDogStatsDServer(t, r)

	send(t, addr, "a:1|c\nbad line\n\nb:2|g|#x:y\r\n_e{1,4}:t|a\\nb\nc:3|h\n")
	require.Eventually(t, func() bool { return len(r.Metrics()) == 3 && len(r.Events()) == 1 },
		5*time.Second, 5*time.Millisecond)

	metrics := r.Metrics()
	names := make([]string, 0, len(metrics))
	for _, m := range metrics {
		names = append(names, m.Name)
	}
	require.Equal(t, []string{"a", "b", "c"}, names)
	require.Equal(t, "a\nb", r.Events()[0].Text)
	require.Equal(t, []string{"x:y"}, r.Metrics()[1].Tags)
}

func TestServerHandlerFunc(t *testing.T) {
	got := make(chan statstest.DogStatsDMessage, 2)
	addr := statstest.NewDogStatsDServer(t, statstest.DogStatsDHandlerFunc(
		func(msg statstest.DogStatsDMessage, _ net.Addr) { got <- msg }))

	send(t, addr, "a:1|c\n_e{1,1}:t|x")
	require.IsType(t, statstest.DogStatsDMetric{}, <-got)
	require.IsType(t, statstest.DogStatsDEvent{}, <-got)
}

func TestServerServeStopsOnClose(t *testing.T) {
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- statstest.DogStatsDServer{}.Serve(conn, &recorder{}) }()

	require.NoError(t, conn.Close())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the connection was closed")
	}
}

func TestServerServeRejectsNilHandler(t *testing.T) {
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer conn.Close()
	require.Error(t, statstest.DogStatsDServer{}.Serve(conn, nil))
}
