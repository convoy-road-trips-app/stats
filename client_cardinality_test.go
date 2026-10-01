package stats

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestNewClient_OTLP_exports_2000_series_and_drop_counter_when_2001st_series_arrives(t *testing.T) {
	// Given: a default-limit client exporting to a real OTLP/HTTP receiver
	received := make(chan *collectormetricspb.ExportMetricsServiceRequest, 256)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request collectormetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- &request
	}))
	defer server.Close()
	// Background exports are bounded by UDPTimeout (100 ms by default); a batch
	// whose export times out under -race never reaches the receiver.
	client, err := NewClient(WithServiceName("cardinality-e2e"), WithUDPTimeout(10*time.Second), WithOTLP(&OTLPConfig{
		Endpoint: strings.TrimPrefix(server.URL, "http://"),
		Insecure: true,
		Protocol: OTLPProtocolHTTP,
	}))
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Shutdown(context.Background())) }()
	ctx := context.Background()
	for i := range 2000 {
		require.NoError(t, client.Counter(ctx, "jobs_total", 1, WithAttribute("job_id", strconv.Itoa(i))))
	}

	// When
	err = client.Counter(ctx, "jobs_total", 1, WithAttribute("job_id", strconv.Itoa(2000)))

	// Then
	require.ErrorIs(t, err, ErrCardinalityLimit)
	seen := map[string]struct{}{}
	var dropped float64
	deadline := time.After(10 * time.Second)
	for len(seen) < 2000 || dropped < 1 {
		select {
		case request := <-received:
			collectJobSeries(request, seen, &dropped)
		case <-deadline:
			t.Fatalf("got %d series and drop counter %v before timeout", len(seen), dropped)
		}
	}
	require.Len(t, seen, 2000)
	require.NotContains(t, seen, strconv.Itoa(2000))
	require.InDelta(t, float64(1), dropped, 0.001)
}

func collectJobSeries(request *collectormetricspb.ExportMetricsServiceRequest, seen map[string]struct{}, dropped *float64) {
	for _, rm := range request.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				for _, point := range m.GetSum().GetDataPoints() {
					switch m.GetName() {
					case "jobs_total":
						seen[attributeValue(point.GetAttributes(), "job_id")] = struct{}{}
					case droppedLabelsMetric:
						*dropped = point.GetAsDouble() // cumulative
					}
				}
			}
		}
	}
}

func attributeValue(attrs []*commonpb.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}
