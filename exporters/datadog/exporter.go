package datadog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/convoy-road-trips-app/stats/exporters"
	"github.com/convoy-road-trips-app/stats/models"
	"github.com/convoy-road-trips-app/stats/serializers"
)

// Exporter sends metrics to Datadog via the DogStatsD protocol over UDP or a
// Unix datagram socket.
type Exporter struct {
	*exporters.BaseExporter
	config     *models.DatadogConfig
	serializer *serializers.DogStatsDSerializer
	packetSize int
}

// NewExporter creates a new Datadog exporter. It keeps its own copy of config.
func NewExporter(config *models.DatadogConfig) (*Exporter, error) {
	if config == nil {
		return nil, fmt.Errorf("datadog config is nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	cfg := *config
	cfg.Tags = slices.Clone(config.Tags)
	cfg.DistributionPrefixes = slices.Clone(config.DistributionPrefixes)

	network, address, err := cfg.ResolveEndpoint()
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create DogStatsD serializer with global tags
	serializer := serializers.NewDogStatsDSerializer(cfg.Tags,
		serializers.WithDistributions(cfg.UseDistributions),
		serializers.WithDistributionPrefixes(cfg.DistributionPrefixes),
	)

	// Create base exporter
	base, err := exporters.NewBaseExporterNetwork("datadog", network, address, serializer)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		BaseExporter: base,
		config:       &cfg,
		serializer:   serializer,
		packetSize:   cfg.PacketSize(),
	}, nil
}

// Export sends metrics to Datadog. Serialized lines are batched into datagrams
// of at most the configured BufferSize and a line is never split across
// datagrams. A line larger than BufferSize is dropped; Export still sends the
// rest and returns an error counting the dropped lines.
func (e *Exporter) Export(ctx context.Context, metrics []*models.Metric) error {
	if !e.config.Enabled || len(metrics) == 0 {
		return nil
	}

	lines, err := e.serializer.Serialize(metrics)
	if err != nil {
		return fmt.Errorf("serialize metrics: %w", err)
	}

	packets, oversized := packetize(lines, e.packetSize)
	sendErr := e.SendPackets(ctx, packets, len(lines)-oversized)

	var dropErr error
	if oversized > 0 {
		dropErr = fmt.Errorf("datadog: dropped %d line(s) larger than buffer size %d", oversized, e.packetSize)
	}
	return errors.Join(sendErr, dropErr)
}

// packetize joins whole lines with newlines into datagrams of at most size
// bytes. Lines that cannot fit in a datagram on their own are skipped and
// counted in oversized.
func packetize(lines [][]byte, size int) (packets [][]byte, oversized int) {
	var cur bytes.Buffer
	flush := func() {
		if cur.Len() > 0 {
			packets = append(packets, bytes.Clone(cur.Bytes()))
			cur.Reset()
		}
	}
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if len(line) > size {
			oversized++
			continue
		}
		if cur.Len() > 0 && cur.Len()+1+len(line) > size {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.Write(line)
	}
	flush()
	return packets, oversized
}
