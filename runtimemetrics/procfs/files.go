package procfs

import "os"

// OpenFileCount returns the number of open file descriptors of pid, counted
// from /proc/<pid>/fd. For the calling process the descriptor used to list the
// directory is not counted.
func (r *Reader) OpenFileCount(pid int) (uint64, error) {
	f, err := os.Open(r.procPath(pid, "fd"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	n := uint64(len(names))
	if pid == r.self() && n > 0 {
		n--
	}
	return n, nil
}

// OpenFileCount counts the open file descriptors of pid with the Default reader.
func OpenFileCount(pid int) (uint64, error) { return Default.OpenFileCount(pid) }
