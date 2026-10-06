package stats

import (
	"context"
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/attribute"

	"github.com/convoy-road-trips-app/stats/models"
)

// datadogExporterName is the name the Datadog exporter registers under.
const datadogExporterName = "datadog"

// eventExporter is implemented by exporters that can deliver Datadog events;
// the built-in Datadog exporter does.
type eventExporter interface {
	SendEvent(ctx context.Context, ev models.DatadogEvent) error
}

// Event sends ev to the Datadog agent as a DogStatsD event, directly over the
// Datadog connection and not through the metric buffer, so it is delivered
// before Event returns. It blocks for at most the configured UDP timeout.
//
// Tags are the view tags, then the tags carried by ctx (ContextWithTags), then
// ev.Tags; on a duplicate key the later one wins. Every key passes the same
// validation as a metric tag, and an invalid key returns ErrInvalidTagKey.
// Datadog Filters and the configured global tags apply as for metrics.
//
// Event returns nil on a disabled client (see Disabled) without validating ev,
// ErrClientClosed once the root client is closed, ErrDatadogNotConfigured when
// no Datadog backend is configured, and ErrEventTooLarge when the serialized
// event is larger than the Datadog BufferSize. A delivery failure (including a
// panic in the exporter, which is recovered) is returned, counted in
// ClientStats.EventsDropped and in Pipeline.ExporterErrors["datadog"]. A view
// sends through the root's connection.
func (c *Client) Event(ctx context.Context, ev DatadogEvent) error { //nolint:gocritic // hugeParam: the public signature takes the event by value
	return c.sendEvent(ctx, &ev)
}

// sendEvent is the event path. ev is the caller's copy and is modified. It takes core.mu.RLock once, like record, but
// never touches the pipeline's buffer.
func (c *Client) sendEvent(ctx context.Context, ev *DatadogEvent) error {
	core := c.core
	if core.disabled {
		return nil
	}
	core.mu.RLock()
	defer core.mu.RUnlock()

	if core.closed {
		return ErrClientClosed
	}
	sender, ok := core.pipeline.exporter(datadogExporterName).(eventExporter)
	if !ok {
		return ErrDatadogNotConfigured
	}

	// resolveAttrs may write into its argument, so give it a copy of the
	// caller's tags; NewSet collapses duplicate keys, keeping the last value.
	attrs := c.resolveAttrs(ctx, slices.Clone(ev.Tags))
	if err := admitKeys(attrs); err != nil {
		return err
	}
	set := attribute.NewSet(attrs...)
	ev.Tags = set.ToSlice()

	if err := c.deliverEvent(ctx, sender, ev); err != nil {
		core.eventsDropped.Add(1)
		core.pipeline.RecordExporterError(datadogExporterName)
		return err
	}
	return nil
}

// deliverEvent sends ev within the UDP timeout and turns an exporter panic
// into an error wrapping ErrExportFailed.
func (c *Client) deliverEvent(ctx context.Context, sender eventExporter, ev *DatadogEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: datadog event: panic: %v", ErrExportFailed, r)
		}
	}()
	if timeout := c.core.cfg.UDPTimeout; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return sender.SendEvent(ctx, *ev)
}
