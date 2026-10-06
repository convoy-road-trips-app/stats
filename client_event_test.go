package stats

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// eventListener is a raw loopback UDP socket standing in for the Datadog agent.
func eventListener(t *testing.T) (conn net.PacketConn, port int) {
	t.Helper()
	conn, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, conn.LocalAddr().(*net.UDPAddr).Port
}

func readDatagram(t *testing.T, conn net.PacketConn) string {
	t.Helper()
	buf := make([]byte, 70000)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, _, err := conn.ReadFrom(buf)
	require.NoError(t, err)
	return string(buf[:n])
}

func newEventClient(t *testing.T, port int, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{
		WithVersionReporting(false),
		WithFlushInterval(time.Hour),
		WithDatadog(&DatadogConfig{AgentHost: "127.0.0.1", AgentPort: port}),
	}, opts...)
	client, err := NewClient(opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// eventStub is a custom exporter named "datadog" whose SendEvent is a seam.
type eventStub struct {
	send func(context.Context, models.DatadogEvent) error
}

func (*eventStub) Name() string                                   { return "datadog" }
func (*eventStub) Export(context.Context, []*models.Metric) error { return nil }
func (*eventStub) Shutdown(context.Context) error                 { return nil }
func (s *eventStub) SendEvent(ctx context.Context, ev models.DatadogEvent) error { //nolint:gocritic // hugeParam: implements eventExporter
	return s.send(ctx, ev)
}

func newStubClient(t *testing.T, send func(context.Context, models.DatadogEvent) error) *Client {
	t.Helper()
	client, err := NewClient(WithVersionReporting(false), WithFlushInterval(time.Hour), WithExporter(&eventStub{send: send}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestEventDelivered(t *testing.T) {
	conn, port := eventListener(t)
	client := newEventClient(t, port)

	err := client.Event(context.Background(), DatadogEvent{
		Title:     "deploy",
		Text:      "line1\nline2",
		Timestamp: time.Unix(1700000000, 0),
		Priority:  EventPriorityLow,
		AlertType: EventAlertTypeSuccess,
		Tags:      []attribute.KeyValue{attribute.String("env", "prod")},
	})
	require.NoError(t, err)

	got := readDatagram(t, conn)
	require.True(t, strings.HasPrefix(got, "_e{6,12}:deploy|line1\\nline2|d:1700000000|p:low|t:success|#"), got)
	require.Contains(t, got, "env:prod")
	require.Zero(t, client.Stats().EventsDropped)
}

func TestEventDatadogNotConfigured(t *testing.T) {
	client, err := NewClient(WithVersionReporting(false), WithFlushInterval(time.Hour),
		WithOTLP(&OTLPConfig{Endpoint: "127.0.0.1:1", Insecure: true}))
	require.NoError(t, err)
	defer client.Close()

	err = client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"})
	require.ErrorIs(t, err, ErrDatadogNotConfigured)
	require.Zero(t, client.Stats().EventsDropped, "not-configured is not a delivery failure")
	require.ErrorIs(t, client.WithPrefix("p").Event(context.Background(), DatadogEvent{}), ErrDatadogNotConfigured)
}

func TestEventTooLarge(t *testing.T) {
	conn, port := eventListener(t)
	client := newEventClient(t, port, WithDatadog(&DatadogConfig{AgentHost: "127.0.0.1", AgentPort: port, BufferSize: 64}))

	err := client.Event(context.Background(), DatadogEvent{Title: "t", Text: strings.Repeat("x", 100)})
	require.ErrorIs(t, err, ErrEventTooLarge)
	require.Equal(t, uint64(1), client.Stats().EventsDropped)
	require.Equal(t, uint64(1), client.Stats().Pipeline.ExporterErrors["datadog"])

	// An event that fits is still sent.
	require.NoError(t, client.Event(context.Background(), DatadogEvent{Title: "t", Text: strings.Repeat("y", 40)}))
	require.Contains(t, readDatagram(t, conn), strings.Repeat("y", 40))
}

func TestEventClosedRoot(t *testing.T) {
	_, port := eventListener(t)
	client := newEventClient(t, port)
	view := client.WithPrefix("v")
	require.NoError(t, client.Close())

	require.ErrorIs(t, client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"}), ErrClientClosed)
	require.ErrorIs(t, view.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"}), ErrClientClosed)
}

func TestEventDisabledReturnsNilWithoutValidating(t *testing.T) {
	client := newDisabledClient(t)
	bad := DatadogEvent{Tags: []attribute.KeyValue{attribute.String("bad key", "v")}}
	require.NoError(t, client.Event(context.Background(), bad))
	require.NoError(t, client.WithTags().Event(context.Background(), bad))
	require.Zero(t, client.Stats().EventsDropped)
}

func TestNoOpClientEvent(t *testing.T) {
	require.NoError(t, NewNoOpClient().Event(context.Background(), DatadogEvent{}))
}

func TestEventTagPrecedence(t *testing.T) {
	var got models.DatadogEvent
	client := newStubClient(t, func(_ context.Context, ev models.DatadogEvent) error {
		got = ev
		return nil
	})
	view := client.WithTags(WithAttribute("a", "view"), WithAttribute("b", "view"), WithAttribute("c", "view")).
		WithTags(WithAttribute("a", "child"))
	ctx := ContextWithTags(context.Background(), attribute.String("b", "ctx"), attribute.String("c", "ctx"))

	original := []attribute.KeyValue{attribute.String("c", "event"), attribute.String("d", "event")}
	require.NoError(t, view.Event(ctx, DatadogEvent{Title: "t", Text: "x", Tags: original}))

	want := map[string]string{"a": "child", "b": "ctx", "c": "event", "d": "event"}
	have := map[string]string{}
	for _, kv := range got.Tags {
		have[string(kv.Key)] = kv.Value.AsString()
	}
	require.Equal(t, want, have)
	require.Len(t, got.Tags, len(want), "duplicate keys collapse")
	require.Equal(t, []attribute.KeyValue{attribute.String("c", "event"), attribute.String("d", "event")}, original,
		"the caller's tags are not modified")
}

func TestEventInvalidTagKey(t *testing.T) {
	called := false
	client := newStubClient(t, func(context.Context, models.DatadogEvent) error { called = true; return nil })

	err := client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x",
		Tags: []attribute.KeyValue{attribute.String("bad key", "v")}})
	require.ErrorIs(t, err, ErrInvalidTagKey)

	err = client.WithTags(WithAttribute("9bad", "v")).Event(context.Background(), DatadogEvent{Title: "t", Text: "x"})
	require.ErrorIs(t, err, ErrInvalidTagKey)

	ctx := ContextWithTags(context.Background(), attribute.String("-", "v"))
	require.ErrorIs(t, client.Event(ctx, DatadogEvent{Title: "t", Text: "x"}), ErrInvalidTagKey)

	require.False(t, called)
	require.Zero(t, client.Stats().EventsDropped)
}

func TestEventFailureCounters(t *testing.T) {
	boom := errors.New("boom")
	client := newStubClient(t, func(context.Context, models.DatadogEvent) error { return boom })

	for range 3 {
		require.ErrorIs(t, client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"}), boom)
	}
	stats := client.Stats()
	require.Equal(t, uint64(3), stats.EventsDropped)
	require.Equal(t, uint64(3), stats.Pipeline.ExporterErrors["datadog"])
}

func TestEventExporterPanicIsRecovered(t *testing.T) {
	var calls int
	client := newStubClient(t, func(context.Context, models.DatadogEvent) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	})

	err := client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"})
	require.ErrorIs(t, err, ErrExportFailed)
	require.ErrorContains(t, err, "boom")
	require.Equal(t, uint64(1), client.Stats().EventsDropped)

	require.NoError(t, client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"}), "client keeps working")
}

func TestEventUsesUDPTimeout(t *testing.T) {
	var deadline time.Time
	client, err := NewClient(WithVersionReporting(false), WithFlushInterval(time.Hour), WithUDPTimeout(250*time.Millisecond),
		WithExporter(&eventStub{send: func(ctx context.Context, _ models.DatadogEvent) error {
			deadline, _ = ctx.Deadline()
			return nil
		}}))
	require.NoError(t, err)
	defer client.Close()

	start := time.Now()
	require.NoError(t, client.Event(context.Background(), DatadogEvent{Title: "t", Text: "x"}))
	require.WithinDuration(t, start.Add(250*time.Millisecond), deadline, 100*time.Millisecond)
}

func TestEventConcurrentWithShutdown(t *testing.T) {
	_, port := eventListener(t)
	client := newEventClient(t, port)
	views := []*Client{client, client.WithPrefix("a"), client.WithTags(WithAttribute("k", "v"))}

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := views[i%len(views)].Event(context.Background(), DatadogEvent{Title: "t" + strconv.Itoa(i), Text: "x"})
			if err != nil {
				assert.ErrorIs(t, err, ErrClientClosed)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.Close()
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: Event and Shutdown did not finish within 5s")
	}
}
