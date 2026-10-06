package statstest

import (
	"fmt"
	"strconv"
	"strings"
)

// String returns the metric as a DogStatsD line,
// <name>:<value>|<type>[|@<rate>][|#<tag>,<tag>...], the form DogStatsDServer
// parses. The "@rate" section is written only when SampleRate is neither 0
// nor 1. A parsed metric formats back to an equivalent line.
//
//nolint:gocritic // hugeParam: value receiver, so both DogStatsDMetric and *DogStatsDMetric print (segmentio parity).
func (m DogStatsDMetric) String() string {
	var b strings.Builder
	b.WriteString(m.Name)
	b.WriteByte(':')
	b.WriteString(strconv.FormatFloat(m.Value, 'g', -1, 64))
	b.WriteByte('|')
	b.WriteString(string(m.Type))
	if m.SampleRate != 0 && m.SampleRate != 1 {
		b.WriteString("|@")
		b.WriteString(strconv.FormatFloat(m.SampleRate, 'g', -1, 64))
	}
	writeFormattedTags(&b, m.Tags)
	return b.String()
}

// Format implements fmt.Formatter; every verb prints String().
//
//nolint:gocritic // hugeParam: value receiver, see String.
func (m DogStatsDMetric) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(m.String()))
}

// String returns the event as a DogStatsD event datagram,
// _e{<title bytes>,<text bytes>}:<title>|<text>[|d:<ts>][|h:<host>]
// [|p:<priority>][|t:<alert>][|k:<aggkey>][|s:<source>][|#tags], the form
// DogStatsDServer parses. Line feeds in the title and text are escaped as a
// backslash and n, and the length prefixes count the escaped bytes; unset
// optional fields are omitted.
//
//nolint:gocritic // hugeParam: value receiver, so both DogStatsDEvent and *DogStatsDEvent print (segmentio parity).
func (e DogStatsDEvent) String() string {
	title := strings.ReplaceAll(e.Title, "\n", `\n`)
	text := strings.ReplaceAll(e.Text, "\n", `\n`)

	var b strings.Builder
	fmt.Fprintf(&b, "_e{%d,%d}:%s|%s", len(title), len(text), title, text)
	if !e.Timestamp.IsZero() {
		b.WriteString("|d:")
		b.WriteString(strconv.FormatInt(e.Timestamp.Unix(), 10))
	}
	for _, f := range []struct{ prefix, value string }{
		{"|h:", e.Host},
		{"|p:", string(e.Priority)},
		{"|t:", string(e.AlertType)},
		{"|k:", e.AggregationKey},
		{"|s:", e.SourceTypeName},
	} {
		if f.value != "" {
			b.WriteString(f.prefix)
			b.WriteString(f.value)
		}
	}
	writeFormattedTags(&b, e.Tags)
	return b.String()
}

// Format implements fmt.Formatter; every verb prints String().
//
//nolint:gocritic // hugeParam: value receiver, see String.
func (e DogStatsDEvent) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(e.String()))
}

func writeFormattedTags(b *strings.Builder, tags []string) {
	if len(tags) == 0 {
		return
	}
	b.WriteString("|#")
	b.WriteString(strings.Join(tags, ","))
}
