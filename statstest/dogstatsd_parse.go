package statstest

import (
	"bytes"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/convoy-road-trips-app/stats/models"
)

// DogStatsDMetricType is the type code of a DogStatsD metric line.
type DogStatsDMetricType string

// The metric type codes DogStatsDServer understands.
const (
	// DogStatsDCounter is a counter ("c").
	DogStatsDCounter DogStatsDMetricType = "c"
	// DogStatsDGauge is a gauge ("g").
	DogStatsDGauge DogStatsDMetricType = "g"
	// DogStatsDHistogram is a histogram ("h").
	DogStatsDHistogram DogStatsDMetricType = "h"
	// DogStatsDDistribution is a distribution ("d").
	DogStatsDDistribution DogStatsDMetricType = "d"
	// DogStatsDSet is a set ("s"). Its value must be numeric.
	DogStatsDSet DogStatsDMetricType = "s"
	// DogStatsDTiming is a timing ("ms").
	DogStatsDTiming DogStatsDMetricType = "ms"
)

// DogStatsDMetric is one metric line received by DogStatsDServer:
//
//	<name>:<value>|<type>[|@<rate>][|#<tag>,<tag>...]
type DogStatsDMetric struct {
	// Name is the metric name.
	Name string
	// Value is the numeric value. Set ("s") values must be numeric too; a
	// line with a non-numeric value is malformed and skipped.
	Value float64
	// Type is the type code: c, g, h, d, s or ms.
	Type DogStatsDMetricType
	// Tags are the "|#" tags, in wire order. Nil when the line has none.
	Tags []string
	// SampleRate is the "@rate" section, in (0, 1]. It is 1 when the line has
	// no "@rate" section.
	SampleRate float64
}

// DogStatsDEvent is one event datagram received by DogStatsDServer:
//
//	_e{<title bytes>,<text bytes>}:<title>|<text>[|d:<ts>][|h:<host>][|p:<priority>][|t:<alert>][|k:<aggkey>][|s:<source>][|#tags]
//
// Escaped line feeds (a backslash followed by n) in Title and Text are
// decoded back to line feeds. Unset optional fields hold their zero value.
type DogStatsDEvent struct {
	// Title is the event title. It is never empty.
	Title string
	// Text is the event text.
	Text string
	// Timestamp is the "d:" section as Unix seconds, or the zero time.
	Timestamp time.Time
	// Host is the "h:" section.
	Host string
	// Priority is the "p:" section.
	Priority models.EventPriority
	// AlertType is the "t:" section.
	AlertType models.EventAlertType
	// AggregationKey is the "k:" section.
	AggregationKey string
	// SourceTypeName is the "s:" section.
	SourceTypeName string
	// Tags are the "|#" tags, in wire order. Nil when the event has none.
	Tags []string
}

// DogStatsDMessage is a DogStatsDMetric or a DogStatsDEvent. It is what a
// DogStatsDHandlerFunc receives.
type DogStatsDMessage interface {
	dogStatsDMessage()
}

func (DogStatsDMetric) dogStatsDMessage() {}
func (DogStatsDEvent) dogStatsDMessage()  {}

// DogStatsDHandler receives the metrics and events DogStatsDServer parses.
// Calls come from the server's read loop one at a time, so a slow handler
// delays later datagrams.
type DogStatsDHandler interface {
	// HandleMetric is called once per valid metric line.
	HandleMetric(m DogStatsDMetric, from net.Addr)
	// HandleEvent is called once per valid event datagram line.
	HandleEvent(e DogStatsDEvent, from net.Addr)
}

// DogStatsDHandlerFunc adapts a function to DogStatsDHandler. The function is
// called with a DogStatsDMetric or a DogStatsDEvent; type-switch on msg.
type DogStatsDHandlerFunc func(msg DogStatsDMessage, from net.Addr)

// HandleMetric calls f(m, from).
func (f DogStatsDHandlerFunc) HandleMetric(m DogStatsDMetric, from net.Addr) { f(m, from) }

// HandleEvent calls f(e, from).
//
//nolint:gocritic // hugeParam: the value signature is the DogStatsDHandler contract (segmentio parity).
func (f DogStatsDHandlerFunc) HandleEvent(e DogStatsDEvent, from net.Addr) { f(e, from) }

// dispatchDatagram splits a datagram into lines and hands every well-formed
// metric or event to h. Malformed lines are skipped silently.
func dispatchDatagram(h DogStatsDHandler, data []byte, from net.Addr) {
	for len(data) > 0 {
		var line []byte
		line, data, _ = bytes.Cut(data, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("_e{")) {
			if e, ok := parseDogStatsDEvent(string(line)); ok {
				h.HandleEvent(e, from)
			}
			continue
		}
		if m, ok := parseDogStatsDMetric(string(line)); ok {
			h.HandleMetric(m, from)
		}
	}
}

// parseDogStatsDMetric parses one metric line. Unknown trailing sections (for
// example "c:" container ids) are ignored.
func parseDogStatsDMetric(line string) (DogStatsDMetric, bool) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok || name == "" {
		return DogStatsDMetric{}, false
	}
	sections := strings.Split(rest, "|")
	if len(sections) < 2 {
		return DogStatsDMetric{}, false
	}
	value, err := strconv.ParseFloat(sections[0], 64)
	if err != nil || math.IsNaN(value) {
		return DogStatsDMetric{}, false
	}
	typ := DogStatsDMetricType(sections[1])
	switch typ {
	case DogStatsDCounter, DogStatsDGauge, DogStatsDHistogram,
		DogStatsDDistribution, DogStatsDSet, DogStatsDTiming:
	default:
		return DogStatsDMetric{}, false
	}

	m := DogStatsDMetric{Name: name, Value: value, Type: typ, SampleRate: 1}
	for _, sec := range sections[2:] {
		switch {
		case strings.HasPrefix(sec, "@"):
			rate, err := strconv.ParseFloat(sec[1:], 64)
			if err != nil || !(rate > 0 && rate <= 1) {
				return DogStatsDMetric{}, false
			}
			m.SampleRate = rate
		case strings.HasPrefix(sec, "#"):
			m.Tags = splitTags(sec[1:])
		}
	}
	return m, true
}

// parseDogStatsDEvent parses one "_e{tl,xl}:title|text|..." line.
func parseDogStatsDEvent(line string) (DogStatsDEvent, bool) {
	header, body, ok := strings.Cut(line[len("_e{"):], "}:")
	if !ok {
		return DogStatsDEvent{}, false
	}
	tl, xl, ok := strings.Cut(header, ",")
	if !ok {
		return DogStatsDEvent{}, false
	}
	titleLen, err1 := strconv.Atoi(tl)
	textLen, err2 := strconv.Atoi(xl)
	if err1 != nil || err2 != nil || titleLen <= 0 || textLen < 0 {
		return DogStatsDEvent{}, false
	}
	// body = title "|" text [ "|" field ... ]
	if len(body) < titleLen+1+textLen || body[titleLen] != '|' {
		return DogStatsDEvent{}, false
	}
	title := body[:titleLen]
	body = body[titleLen+1:]
	text := body[:textLen]
	body = body[textLen:]
	if body != "" && body[0] != '|' {
		return DogStatsDEvent{}, false
	}

	e := DogStatsDEvent{Title: unescapeEvent(title), Text: unescapeEvent(text)}
	for sec := range strings.SplitSeq(body, "|") {
		if !e.setField(sec) {
			return DogStatsDEvent{}, false
		}
	}
	return e, true
}

// setField applies one optional "|" section to e. It reports false when the
// section is recognised but invalid; unknown sections are ignored.
func (e *DogStatsDEvent) setField(sec string) bool {
	switch {
	case strings.HasPrefix(sec, "d:"):
		ts, err := strconv.ParseInt(sec[2:], 10, 64)
		if err != nil {
			return false
		}
		e.Timestamp = time.Unix(ts, 0)
	case strings.HasPrefix(sec, "h:"):
		e.Host = sec[2:]
	case strings.HasPrefix(sec, "p:"):
		e.Priority = models.EventPriority(sec[2:])
	case strings.HasPrefix(sec, "t:"):
		e.AlertType = models.EventAlertType(sec[2:])
	case strings.HasPrefix(sec, "k:"):
		e.AggregationKey = sec[2:]
	case strings.HasPrefix(sec, "s:"):
		e.SourceTypeName = sec[2:]
	case strings.HasPrefix(sec, "#"):
		e.Tags = splitTags(sec[1:])
	}
	return true
}

// unescapeEvent decodes the escaped line feeds of an event title or text.
func unescapeEvent(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

// splitTags splits a comma separated tag list, dropping empty entries. It
// returns nil when no tag remains.
func splitTags(s string) []string {
	var tags []string
	for tag := range strings.SplitSeq(s, ",") {
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}
