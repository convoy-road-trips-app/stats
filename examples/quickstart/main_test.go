package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The usage guide shows main.go verbatim; this keeps the copy-pasteable
// snippet identical to the program that `go build ./examples/...` compiles.
func TestUsageGuide_quickstart_snippet_is_main_go(t *testing.T) {
	// Given
	source, err := os.ReadFile("main.go")
	require.NoError(t, err)
	guide, err := os.ReadFile("../../docs/usage.md")
	require.NoError(t, err)

	// Then
	snippet := "```go\n" + strings.TrimSpace(string(source)) + "\n```"
	require.Contains(t, string(guide), snippet, "docs/usage.md must contain examples/quickstart/main.go verbatim")
}
