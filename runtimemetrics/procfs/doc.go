// Package procfs exposes read-only parsers and readers for the Linux /proc and
// cgroup files that process metrics are built from: stat, statm, sched,
// limits, status, meminfo, cgroup membership, cgroup CPU and memory limits and
// open file counts.
//
// It is the counterpart of segmentio's procstats/linux package. Every file
// format has a Parse function over []byte that works on any platform (so it
// can be tested with fixtures on a developer laptop), and a Reader method that
// reads the file for a PID. The package-level Read functions use the real
// /proc and /sys/fs/cgroup; a Reader with other roots reads a mounted host
// procfs or a fixture tree.
//
// Parsers never panic on malformed input; they return an error instead.
// Readers on a platform without procfs fail with the underlying file error.
// The package has no dependencies outside the standard library.
package procfs
