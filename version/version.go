// Package version reports the version of this library and of the Go toolchain
// that built the program, as read from the build information embedded in the
// binary. It has no dependencies beyond the standard library.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// ModulePath is the module whose version Version reports.
const ModulePath = "github.com/convoy-road-trips-app/stats"

// Unknown is what Version returns when the build information carries no
// version for the module, as for a local build of the module itself.
const Unknown = "(devel)"

var (
	moduleOnce sync.Once
	moduleVsn  string

	goOnce sync.Once
	goVsn  string
)

// Version returns the module version of this library from the build
// information (for example "v1.4.0"), or Unknown when it is not recorded. A
// replace directive that names a version takes precedence over the required
// one. The result is computed once.
func Version() string {
	moduleOnce.Do(func() {
		moduleVsn = moduleVersion(debug.ReadBuildInfo())
	})
	return moduleVsn
}

// moduleVersion extracts the version of ModulePath from info.
func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return Unknown
	}
	vsn := Unknown
	if info.Main.Path == ModulePath && info.Main.Version != "" {
		vsn = info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path != ModulePath {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
		break
	}
	return vsn
}

// GoVersion returns the Go version that built the program in a form that is
// easy to use as a metric attribute: runtime.Version() without the leading
// "go", for example "1.25.5". It is computed once.
func GoVersion() string {
	goOnce.Do(func() {
		goVsn = strings.TrimPrefix(runtime.Version(), "go")
	})
	return goVsn
}

// DevelGoVersion reports whether the Go toolchain is a development ("tip")
// build. Such versions embed a commit hash and change constantly, so they make
// poor metric attributes.
func DevelGoVersion() bool {
	return isDevel(GoVersion())
}

func isDevel(vsn string) bool {
	return strings.Count(vsn, " ") > 2 || strings.HasPrefix(vsn, "devel")
}
