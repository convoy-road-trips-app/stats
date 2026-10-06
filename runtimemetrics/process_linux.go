//go:build linux

package runtimemetrics

import "os"

// newProcessSource reads the real /proc and /sys files.
func newProcessSource() *processSource {
	return &processSource{
		readFile: os.ReadFile,
		countDir: countDirEntries,
	}
}

// countDirEntries counts the entries of a directory. For /proc/self/fd this
// excludes the descriptor opened to list the directory itself.
func countDirEntries(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	n := len(names)
	if path == procFDDir && n > 0 {
		n--
	}
	return n, nil
}
