package prometheus

import (
	"bytes"
	"testing"
)

func render(t *testing.T, families ...Family) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteFamilies(&buf, families); err != nil {
		t.Fatalf("WriteFamilies: %v", err)
	}
	return buf.String()
}
