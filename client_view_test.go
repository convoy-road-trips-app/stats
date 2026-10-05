package stats

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestWithPrefixJoinsNames(t *testing.T) {
	// Given: nested prefixes, one of them empty
	client, p := unstartedClient(t)
	child := client.WithPrefix("api").WithPrefix("").WithPrefix("v1")

	// When
	require.NoError(t, child.Counter(context.Background(), "req", 1))

	// Then: the parts are joined with "." and the parent is unprefixed
	require.Equal(t, "api.v1.req", bufferedMetric(t, p).Name)
	require.NoError(t, client.Counter(context.Background(), "req", 1))
	require.Equal(t, "req", bufferedMetric(t, p).Name)
}

func TestWithPrefixAppliesToRecordMetricWithoutLeakingIntoCallerMetric(t *testing.T) {
	client, p := unstartedClient(t)
	child := client.WithPrefix("api")
	m := NewCounter("req", 1).Build()

	require.NoError(t, child.RecordMetric(context.Background(), m))

	require.Equal(t, "api.req", bufferedMetric(t, p).Name)

	// A failed recording leaves the caller's metric name untouched.
	long := NewCounter("x", 1).Build()
	require.Error(t, child.WithPrefix(strings.Repeat("p", 300)).RecordMetric(context.Background(), long))
	require.Equal(t, "x", long.Name)
}

func TestChildCloseDoesNotCloseParent(t *testing.T) {
	// Given: a real root and a child view
	root, err := NewClient(WithServiceName("view-close"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	child := root.WithPrefix("api").WithTags(WithAttribute("k", "v"))

	// When: the child is closed and shut down
	require.NoError(t, child.Close())
	require.NoError(t, child.Shutdown(context.Background()))

	// Then: the root still records, flushes and reports open; so does the child
	ctx := context.Background()
	require.NoError(t, root.Counter(ctx, "still_open", 1))
	require.NoError(t, child.Counter(ctx, "still_open", 1))
	require.NoError(t, child.Flush(ctx))
	require.False(t, root.Stats().Closed)
	require.False(t, child.Stats().Closed)
}

func TestChildStatsAreRootStats(t *testing.T) {
	root, err := NewClient(WithServiceName("view-stats"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })

	require.Equal(t, "view-stats", root.WithPrefix("a").Stats().ServiceName)
}

func TestChildRecordAfterRootCloseReturnsErrClientClosed(t *testing.T) {
	root, err := NewClient(WithServiceName("view-after-close"))
	require.NoError(t, err)
	child := root.WithPrefix("api")
	require.NoError(t, root.Close())

	require.ErrorIs(t, child.Counter(context.Background(), "req", 1), ErrClientClosed)
	require.ErrorIs(t, child.RecordMetric(context.Background(), NewCounter("req", 1).Build()), ErrClientClosed)
	require.ErrorIs(t, child.Flush(context.Background()), ErrClientClosed)
	require.True(t, child.Stats().Closed)
}

func TestWithTagsChildWins(t *testing.T) {
	// Given: a parent tag, a child tag with the same key, and a sibling-only tag
	client, p := unstartedClient(t)
	parent := client.WithTags(WithAttribute("env", "parent"), WithAttribute("keep", "yes"))
	child := parent.WithTags(WithAttribute("env", "child"))

	// When
	require.NoError(t, child.Counter(context.Background(), "m", 1))

	// Then: the child's value wins and the parent's other tag is kept
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("env", "child"), attribute.String("keep", "yes"),
	}, bufferedMetric(t, p).Attributes)

	// And: the parent view is unchanged
	require.NoError(t, parent.Counter(context.Background(), "m", 1))
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("env", "parent"), attribute.String("keep", "yes"),
	}, bufferedMetric(t, p).Attributes)
}

func TestWithTagsKeepsPrefix(t *testing.T) {
	client, p := unstartedClient(t)

	require.NoError(t, client.WithPrefix("api").WithTags(WithAttribute("k", "v")).Counter(context.Background(), "req", 1))

	require.Equal(t, "api.req", bufferedMetric(t, p).Name)
}

func TestViewTagPrecedence(t *testing.T) {
	// Given: the same key set by view, context, the metric itself and an option
	client, p := unstartedClient(t)
	view := client.WithTags(WithAttribute("a", "view"), WithAttribute("b", "view"), WithAttribute("c", "view"), WithAttribute("d", "view"))
	ctx := ContextWithTags(context.Background(),
		attribute.String("b", "ctx"), attribute.String("c", "ctx"), attribute.String("d", "ctx"))
	m := NewCounter("m", 1).WithTag("c", "metric").WithTag("d", "metric").Build()

	// When
	require.NoError(t, view.RecordMetric(ctx, m))

	// Then: view < context < metric
	got := bufferedMetric(t, p).Attributes
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("a", "view"), attribute.String("b", "ctx"),
		attribute.String("c", "metric"), attribute.String("d", "metric"),
	}, got)

	// And: an explicit option beats all of them
	require.NoError(t, view.Counter(ctx, "m", 1, WithAttribute("a", "opt"), WithAttribute("b", "opt")))
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("a", "opt"), attribute.String("b", "opt"),
		attribute.String("c", "ctx"), attribute.String("d", "ctx"),
	}, bufferedMetric(t, p).Attributes)
}

func TestPrefixCountsTowardNameLimit(t *testing.T) {
	// Given: a name that fits alone but not after the prefix
	client, p := unstartedClient(t)
	name := strings.Repeat("n", 250)
	require.NoError(t, client.Counter(context.Background(), name, 1))
	require.Equal(t, name, bufferedMetric(t, p).Name)

	// When
	err := client.WithPrefix("0123456789").Counter(context.Background(), name, 1)

	// Then: the full name is over the 256-character limit
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Empty(t, p.buffer.PopBatch(1))
}

func TestViewsRecordWhileRootShutsDown(t *testing.T) {
	// Given: 100 goroutines recording on children of one root
	root, err := NewClient(WithServiceName("view-shutdown"))
	require.NoError(t, err)
	children := []*Client{
		root.WithPrefix("a"),
		root.WithTags(WithAttribute("k", "v")),
		root.WithPrefix("b").WithTags(WithAttribute("k", "v")),
	}

	const goroutines = 100
	var started, finished sync.WaitGroup
	started.Add(goroutines)
	finished.Add(goroutines)
	stop := make(chan struct{})
	for i := range goroutines {
		go func() {
			defer finished.Done()
			child := children[i%len(children)]
			started.Done()
			ctx := context.Background()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = child.Counter(ctx, "c", 1)
				_ = child.RecordMetric(ctx, NewCounter("r", 1).Build())
				_ = child.Stats()
			}
		}()
	}
	started.Wait()

	// When: the root shuts down while they record
	done := make(chan error, 1)
	go func() { done <- root.Shutdown(context.Background()) }()

	// Then: shutdown and every recorder finish within 5s
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s")
	}
	require.ErrorIs(t, children[0].Counter(context.Background(), "after", 1), ErrClientClosed)
	close(stop)

	finishedCh := make(chan struct{})
	go func() { finished.Wait(); close(finishedCh) }()
	select {
	case <-finishedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("recording goroutines did not stop within 5s")
	}
}

func TestNoOpClientViewsReturnSameClient(t *testing.T) {
	n := NewNoOpClient()

	require.Same(t, n, n.WithPrefix("api", WithAttribute("k", "v")))
	require.Same(t, n, n.WithTags(WithAttribute("k", "v")))
}
