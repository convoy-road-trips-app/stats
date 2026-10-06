package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoVersionTrimsPrefix(t *testing.T) {
	require.Equal(t, strings.TrimPrefix(runtime.Version(), "go"), GoVersion())
	require.False(t, strings.HasPrefix(GoVersion(), "go"))
}

func TestIsDevel(t *testing.T) {
	require.False(t, isDevel("1.25.5"))
	require.False(t, isDevel("1.25rc1"))
	require.True(t, isDevel("devel +abc123 Mon Jan 2 15:04:05 2006 +0000"))
	require.True(t, isDevel("devel go1.26-abc"))
	require.True(t, isDevel("1.26-abc Mon Jan 2 15:04:05 2006"))
}

func TestDevelGoVersionMatchesRuntime(t *testing.T) {
	require.Equal(t, isDevel(GoVersion()), DevelGoVersion())
}

func TestVersionIsStable(t *testing.T) {
	first := Version()
	require.NotEmpty(t, first)
	require.Equal(t, first, moduleVsn)
}

func TestModuleVersion(t *testing.T) {
	dep := func(version string, replace *debug.Module) *debug.Module {
		return &debug.Module{Path: ModulePath, Version: version, Replace: replace}
	}
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, Unknown},
		{"main module", &debug.BuildInfo{Main: debug.Module{Path: ModulePath, Version: "v1.2.3"}}, true, "v1.2.3"},
		{"main module without version", &debug.BuildInfo{Main: debug.Module{Path: ModulePath}}, true, Unknown},
		{"dependency", &debug.BuildInfo{Deps: []*debug.Module{dep("v1.4.0", nil)}}, true, "v1.4.0"},
		{"replaced with version", &debug.BuildInfo{Deps: []*debug.Module{dep("v1.4.0", &debug.Module{Version: "v1.5.0"})}}, true, "v1.5.0"},
		{"replaced by path", &debug.BuildInfo{Deps: []*debug.Module{dep("v1.4.0", &debug.Module{Path: "../stats"})}}, true, "v1.4.0"},
		{"absent", &debug.BuildInfo{Deps: []*debug.Module{{Path: "other", Version: "v9"}}}, true, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, moduleVersion(tt.info, tt.ok))
		})
	}
}
