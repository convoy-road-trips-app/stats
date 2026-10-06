package serializers

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/convoy-road-trips-app/stats/models"
)

// eventNewlineEscaper rewrites each line feed as the two-byte sequence `\n`.
var eventNewlineEscaper = strings.NewReplacer("\n", `\n`)

// SerializeEvent encodes e as a DogStatsD event datagram:
//
//	_e{<title bytes>,<text bytes>}:<title>|<text>|d:<ts>|h:<host>|p:<priority>|t:<alert>|k:<aggkey>|s:<source>|#tags
//
// Line feeds in the title and text are escaped as a literal backslash-n, and
// the two length prefixes count the escaped bytes. Optional fields that are
// empty (zero timestamp, empty strings, no surviving tags) are omitted. Tags
// follow the metric tag rules: global tags first, then e.Tags, with the
// configured tag filters applied to both. The result is a fresh slice.
func (s *DogStatsDSerializer) SerializeEvent(e *models.DatadogEvent) []byte {
	title := eventNewlineEscaper.Replace(e.Title)
	text := eventNewlineEscaper.Replace(e.Text)

	buf := s.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer s.bufferPool.Put(buf)

	buf.WriteString("_e{")
	buf.WriteString(strconv.Itoa(len(title)))
	buf.WriteByte(',')
	buf.WriteString(strconv.Itoa(len(text)))
	buf.WriteString("}:")
	buf.WriteString(title)
	buf.WriteByte('|')
	buf.WriteString(text)

	if !e.Timestamp.IsZero() {
		buf.WriteString("|d:")
		buf.WriteString(strconv.FormatInt(e.Timestamp.Unix(), 10))
	}
	writeEventField(buf, "|h:", e.Host)
	writeEventField(buf, "|p:", string(e.Priority))
	writeEventField(buf, "|t:", string(e.AlertType))
	writeEventField(buf, "|k:", e.AggregationKey)
	writeEventField(buf, "|s:", e.SourceTypeName)
	s.writeTags(buf, e.Tags)

	packet := make([]byte, buf.Len())
	copy(packet, buf.Bytes())
	return packet
}

// writeEventField appends prefix+value when value is non-empty.
func writeEventField(buf *bytes.Buffer, prefix, value string) {
	if value == "" {
		return
	}
	buf.WriteString(prefix)
	buf.WriteString(value)
}
