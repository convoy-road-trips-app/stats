//go:build integration

package lgtm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/convoy-road-trips-app/stats"
)

var errProbePending = errors.New("probe series not yet queryable")

// TestMain blocks until a probe gauge exported over OTLP is queryable in
// Prometheus. Docker's port proxy accepts TCP before the collector serves
// gRPC, so an open port does not prove the collector -> Prometheus route.
func TestMain(m *testing.M) {
	if err := waitForIngest(readyTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "LGTM stack not ready: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func waitForIngest(timeout time.Duration) error {
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	query := fmt.Sprintf(`lgtm_readiness_probe{run_id=%q}`, runID)
	deadline := time.Now().Add(timeout)
	lastErr := errProbePending
	for time.Now().Before(deadline) {
		lastErr = exportProbe(runID)
		if lastErr == nil {
			lastErr = probeQueryable(query)
			if lastErr == nil {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("%s after %v: %w", query, timeout, lastErr)
}

func exportProbe(runID string) error {
	client, err := stats.NewClient(otlpOptions("lgtm-readiness")...)
	if err != nil {
		return err
	}
	err = client.Gauge(context.Background(), "lgtm_readiness_probe", 1, stats.WithAttribute("run_id", runID))
	return errors.Join(err, client.Close())
}

func probeQueryable(query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := prometheusURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result promResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if len(result.Data.Result) == 0 {
		return errProbePending
	}
	return nil
}
