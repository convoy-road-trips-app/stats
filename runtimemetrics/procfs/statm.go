package procfs

import "strings"

// Statm holds /proc/<pid>/statm. All values are in pages; multiply by
// os.Getpagesize() for bytes.
type Statm struct {
	Size     uint64 // (1) total program size, including virtual mappings
	Resident uint64 // (2) resident set size
	Shared   uint64 // (3) resident shared pages (file backed)
	Text     uint64 // (4) code
	Lib      uint64 // (5) library, always 0 on Linux 2.6+
	Data     uint64 // (6) data and stack
	Dirty    uint64 // (7) dirty pages, always 0 on Linux 2.6+
}

// ParseStatm parses /proc/<pid>/statm.
func ParseStatm(b []byte) (Statm, error) {
	var s Statm
	err := parseUints("statm", strings.Fields(string(b)),
		&s.Size, &s.Resident, &s.Shared, &s.Text, &s.Lib, &s.Data, &s.Dirty)
	if err != nil {
		return Statm{}, err
	}
	return s, nil
}

// ReadStatm reads and parses /proc/<pid>/statm.
func (r *Reader) ReadStatm(pid int) (Statm, error) {
	b, err := r.readProc(pid, "statm")
	if err != nil {
		return Statm{}, err
	}
	return ParseStatm(b)
}

// ReadStatm reads /proc/<pid>/statm with the Default reader.
func ReadStatm(pid int) (Statm, error) { return Default.ReadStatm(pid) }
