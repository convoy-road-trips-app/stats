//go:build docker

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Host ports published by docker-compose.yml.
const (
	prometheusURL   = "http://127.0.0.1:19090"
	collectorMetric = "http://127.0.0.1:18889/metrics"
	pollInterval    = 2 * time.Second
	pollTimeout     = 3 * time.Minute
	composeTimeout  = 10 * time.Minute
)

// wantLE is the numeric le bound of every _bucket series: the default bounds
// and +Inf. Prometheus renders a whole number as "1.0", so labels are parsed.
var wantLE = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, math.Inf(1)}

func compose(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func get(t *testing.T, target string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

// bucketBounds returns the sorted distinct le bounds of the demo's _bucket
// series; there is one series per route for each bound.
func bucketBounds(t *testing.T) ([]float64, error) {
	t.Helper()
	query := histogramName + `_bucket{service_name="stats-docker-example"}`
	body, err := get(t, prometheusURL+"/api/v1/query?query="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return nil, fmt.Errorf("decode %q: %w", body, err)
	}
	var bounds []float64
	for _, series := range response.Data.Result {
		bound, err := strconv.ParseFloat(series.Metric["le"], 64)
		if err != nil {
			return nil, fmt.Errorf("le label of %v: %w", series.Metric, err)
		}
		bounds = append(bounds, bound)
	}
	slices.Sort(bounds)
	return slices.Compact(bounds), nil
}

func TestDockerExample_serves_histogram_bucket_series(t *testing.T) {
	// Given: the compose stack is built and started, and torn down afterwards
	t.Cleanup(func() { compose(t, "down", "--volumes", "--remove-orphans") })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("compose logs:\n%s", compose(t, "logs", "--no-color", "--tail=60"))
		}
	})
	compose(t, "up", "--build", "--detach")

	// When: the demo has exported for a while
	var bounds []float64
	var lastErr error
	require.Eventually(t, func() bool {
		bounds, lastErr = bucketBounds(t)
		return lastErr == nil && slices.Equal(bounds, wantLE)
	}, pollTimeout, pollInterval, "no _bucket series for every bound within %v", pollTimeout)
	require.NoError(t, lastErr)

	// Then: Prometheus serves every bound, and the collector exposes the same series
	require.Equal(t, wantLE, bounds)
	metrics, err := get(t, collectorMetric)
	require.NoError(t, err)
	require.Contains(t, metrics, histogramName+`_bucket{`)
	require.Contains(t, metrics, histogramName+`_count{`)
}
