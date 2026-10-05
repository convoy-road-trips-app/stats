package stats

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// unstartedClient returns a root client over an unstarted pipeline so tests can
// read exactly what was buffered.
func unstartedClient(t *testing.T) (*Client, *Pipeline) {
	t.Helper()
	cfg := DefaultConfig()
	p := newUnstartedPipeline(t, cfg)
	return &Client{core: &clientCore{cfg: cfg, pipeline: p}, root: true}, p
}

func TestContextTagsAppliedToCounter(t *testing.T) {
	// Given: a context carrying tags
	client, p := unstartedClient(t)
	ctx := ContextWithTags(context.Background(),
		attribute.String("region", "eu"), attribute.String("tier", "gold"))

	// When
	require.NoError(t, client.Counter(ctx, "orders_total", 1))

	// Then: the context tags are on the observation
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("region", "eu"), attribute.String("tier", "gold"),
	}, bufferedAttributes(t, p))
}

func TestContextTagsAppliedToEveryRecordingMethod(t *testing.T) {
	ctx := ContextWithTags(context.Background(), attribute.String("region", "eu"))
	for name, record := range map[string]func(*Client) error{
		"gauge":     func(c *Client) error { return c.Gauge(ctx, "m", 1) },
		"histogram": func(c *Client) error { return c.Histogram(ctx, "m", 1) },
		"record":    func(c *Client) error { return c.RecordMetric(ctx, NewCounter("m", 1).Build()) },
	} {
		t.Run(name, func(t *testing.T) {
			client, p := unstartedClient(t)
			require.NoError(t, record(client))
			require.Equal(t, []attribute.KeyValue{attribute.String("region", "eu")}, bufferedAttributes(t, p))
		})
	}
}

func TestExplicitOptionOverridesContextTag(t *testing.T) {
	// Given: a context tag and an explicit option for the same key
	client, p := unstartedClient(t)
	ctx := ContextWithTags(context.Background(),
		attribute.String("region", "eu"), attribute.String("tier", "gold"))

	// When
	require.NoError(t, client.Counter(ctx, "orders_total", 1, WithAttribute("region", "us")))

	// Then: the option wins and the key appears once
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("region", "us"), attribute.String("tier", "gold"),
	}, bufferedAttributes(t, p))
}

func TestMetricAttributesOverrideContextTag(t *testing.T) {
	// Given: a pre-built metric whose attribute collides with a context tag
	client, p := unstartedClient(t)
	ctx := ContextWithTags(context.Background(), attribute.String("region", "eu"))

	// When
	require.NoError(t, client.RecordMetric(ctx, NewCounter("m", 1).WithTag("region", "us").Build()))

	// Then: the metric's own attribute wins
	require.Equal(t, []attribute.KeyValue{attribute.String("region", "us")}, bufferedAttributes(t, p))
}

func TestContextWithTagsDoesNotInheritParentTags(t *testing.T) {
	parent := ContextWithTags(context.Background(), attribute.String("a", "1"))
	child := ContextWithTags(parent, attribute.String("b", "2"))

	require.Equal(t, []attribute.KeyValue{attribute.String("b", "2")}, ContextTags(child))
	require.Equal(t, []attribute.KeyValue{attribute.String("a", "1")}, ContextTags(parent))
}

func TestContextTagsReturnsCopy(t *testing.T) {
	ctx := ContextWithTags(context.Background(), attribute.String("a", "1"))

	got := ContextTags(ctx)
	got[0] = attribute.String("a", "changed")

	require.Equal(t, []attribute.KeyValue{attribute.String("a", "1")}, ContextTags(ctx))
	require.Nil(t, ContextTags(context.Background()))
}

func TestContextAddTagsAppendsInPlace(t *testing.T) {
	ctx := ContextWithTags(context.Background(), attribute.String("a", "1"))
	derived, cancel := context.WithCancel(ctx)
	defer cancel()

	require.True(t, ContextAddTags(ctx, attribute.String("b", "2")))

	require.Equal(t, []attribute.KeyValue{attribute.String("a", "1"), attribute.String("b", "2")}, ContextTags(derived))
}

func TestContextAddTagsWithoutTagsReturnsFalse(t *testing.T) {
	ctx := context.Background()

	require.False(t, ContextAddTags(ctx, attribute.String("a", "1")))
	require.Nil(t, ContextTags(ctx))
}

func TestContextAddTagsConcurrent(t *testing.T) {
	// Given: a shared tag set, concurrent writers and readers
	ctx := ContextWithTags(context.Background())
	client, _ := unstartedClient(t)
	const writers = 50
	var wg sync.WaitGroup
	for range writers {
		wg.Add(3)
		go func() {
			defer wg.Done()
			assert.True(t, ContextAddTags(ctx, attribute.String("k", "v")))
		}()
		go func() {
			defer wg.Done()
			_ = ContextTags(ctx)
		}()
		go func() {
			defer wg.Done()
			_ = client.Counter(ctx, "m", 1)
		}()
	}

	// When / Then: no race, and every append landed
	wg.Wait()
	require.Len(t, ContextTags(ctx), writers)
}

func TestContextTagInvalidKeyRejectsObservation(t *testing.T) {
	// Given: a context tag whose key is invalid
	client, p := unstartedClient(t)
	ctx := ContextWithTags(context.Background(), attribute.String("bad-key", "x"))

	// When
	err := client.Counter(ctx, "orders_total", 1)

	// Then: typed error and nothing buffered
	require.ErrorIs(t, err, ErrInvalidTagKey)
	require.Empty(t, p.buffer.PopBatch(1))
}
