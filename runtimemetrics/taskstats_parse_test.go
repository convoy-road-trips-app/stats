package runtimemetrics

import (
	"encoding/binary"
	"errors"
	"math"
	"syscall"
	"testing"
	"time"
)

const testSeq = 42

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// attr builds an nlattr with value padded to 4 bytes (length excludes padding).
func attr(typ uint16, val []byte) []byte {
	b := make([]byte, 4, 4+len(val)+3)
	binary.NativeEndian.PutUint16(b[0:2], uint16(4+len(val)))
	binary.NativeEndian.PutUint16(b[2:4], typ)
	return pad4(append(b, val...))
}

// nlmsg builds a netlink message with the given type, sequence and payload.
func nlmsg(typ uint16, seq uint32, payload []byte) []byte {
	b := make([]byte, 16, 16+len(payload)+3)
	binary.NativeEndian.PutUint32(b[0:4], uint32(16+len(payload)))
	binary.NativeEndian.PutUint16(b[4:6], typ)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	return pad4(append(b, payload...))
}

// taskstatsPayload builds a struct taskstats of size n with the given version
// and delay totals at the kernel offsets.
func taskstatsPayload(version uint16, n int, cpu, blk, swap, free uint64) []byte {
	p := make([]byte, n)
	binary.NativeEndian.PutUint16(p[0:2], version)
	put := func(off int, v uint64) {
		if off+8 <= n {
			binary.NativeEndian.PutUint64(p[off:], v)
		}
	}
	put(24, cpu)
	put(40, blk)
	put(56, swap)
	put(320, free)
	return p
}

// dataMsg wraps nested attrs in AGGR_TGID inside a genetlink data message.
func dataMsg(seq uint32, top []byte, nested ...[]byte) []byte {
	var inner []byte
	for _, n := range nested {
		inner = append(inner, n...)
	}
	payload := append([]byte{2, 1, 0, 0}, top...) // genlmsghdr: cmd NEW, version 1
	payload = append(payload, attr(5, inner)...)
	return nlmsg(0x18, seq, payload)
}

func tgidAttr() []byte { return attr(2, []byte{1, 0, 0, 0}) }

func errMsg(seq uint32, code int32) []byte {
	p := make([]byte, 4+16)
	binary.NativeEndian.PutUint32(p[0:4], uint32(code))
	return nlmsg(2, seq, p)
}

func TestParseTaskstatsReply(t *testing.T) {
	want := DelayInfo{CPU: 1000, BlockIO: 2000, SwapIn: 3000, FreePages: 4000}
	full := taskstatsPayload(14, 400, 1000, 2000, 3000, 4000)
	oddAttr := attr(1, []byte{1, 2, 3, 4, 5}) // 9-byte attr, 3 bytes of padding

	tests := []struct {
		name      string
		buf       []byte
		wantInfo  DelayInfo
		wantFound bool
		wantErr   error
	}{
		{"valid", dataMsg(testSeq, nil, tgidAttr(), attr(3, full)), want, true, nil},
		{"empty buffer", nil, DelayInfo{}, false, nil},
		{
			"wrong sequence ignored",
			dataMsg(testSeq+1, nil, tgidAttr(), attr(3, full)),
			DelayInfo{}, false, nil,
		},
		{
			"wrong sequence then right",
			append(dataMsg(testSeq+1, nil, attr(3, taskstatsPayload(14, 400, 9, 9, 9, 9))), dataMsg(testSeq, nil, attr(3, full))...),
			want, true, nil,
		},
		{"ack ignored", append(errMsg(testSeq, 0), dataMsg(testSeq, nil, attr(3, full))...), want, true, nil},
		{"ack only", errMsg(testSeq, 0), DelayInfo{}, false, nil},
		{"EPERM", errMsg(testSeq, -int32(syscall.EPERM)), DelayInfo{}, false, syscall.EPERM},
		{"error other seq ignored", errMsg(testSeq+1, -int32(syscall.EPERM)), DelayInfo{}, false, nil},
		{
			"version 5 has no freepages",
			dataMsg(testSeq, nil, attr(3, taskstatsPayload(5, 200, 1000, 2000, 3000, 4000))),
			DelayInfo{CPU: 1000, BlockIO: 2000, SwapIn: 3000}, true, nil,
		},
		{
			"version 7 short for freepages",
			dataMsg(testSeq, nil, attr(3, taskstatsPayload(7, 200, 1, 2, 3, 4))),
			DelayInfo{}, false, errTaskstatsShort,
		},
		{
			"payload shorter than swapin",
			dataMsg(testSeq, nil, attr(3, taskstatsPayload(1, 60, 1, 2, 3, 4))),
			DelayInfo{}, false, errTaskstatsShort,
		},
		{
			"odd-length unknown attribute before AGGR_TGID",
			dataMsg(testSeq, oddAttr, tgidAttr(), attr(3, full)),
			want, true, nil,
		},
		{
			"NULL pad inside nest",
			dataMsg(testSeq, nil, attr(6, []byte{0, 0, 0, 0}), tgidAttr(), attr(6, nil), attr(3, full)),
			want, true, nil,
		},
		{
			"NESTED flag bit on AGGR_TGID",
			func() []byte {
				b := dataMsg(testSeq, nil, attr(3, full))
				// AGGR_TGID attr starts after nlmsghdr(16)+genl(4); set NLA_F_NESTED.
				binary.NativeEndian.PutUint16(b[16+4+2:], 5|1<<15)
				return b
			}(),
			want, true, nil,
		},
		{
			"saturating ns",
			dataMsg(testSeq, nil, attr(3, taskstatsPayload(14, 400, math.MaxUint64, 0, 0, 0))),
			DelayInfo{CPU: time.Duration(math.MaxInt64)}, true, nil,
		},
		{"no stats attr", dataMsg(testSeq, nil, tgidAttr()), DelayInfo{}, false, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := ParseTaskstatsReply(tc.buf, testSeq)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if found != tc.wantFound || got != tc.wantInfo {
				t.Fatalf("got (%+v, %v), want (%+v, %v)", got, found, tc.wantInfo, tc.wantFound)
			}
		})
	}
}

func TestParseTruncatedMessage(t *testing.T) {
	full := dataMsg(testSeq, nil, tgidAttr(), attr(3, taskstatsPayload(14, 400, 1, 2, 3, 4)))
	// Every strict prefix must return without panicking; those cutting a
	// header or attribute short must report an error.
	for n := range full {
		_, found, err := ParseTaskstatsReply(full[:n], testSeq)
		if found {
			t.Fatalf("prefix %d: unexpectedly found", n)
		}
		if n > 0 && err == nil {
			t.Fatalf("prefix %d: want error", n)
		}
		if err != nil && !errors.Is(err, errTaskstatsShort) {
			t.Fatalf("prefix %d: err = %v, want errTaskstatsShort", n, err)
		}
	}

	// Declared lengths that are too small or run past the buffer.
	bad := map[string][]byte{
		"msg len below header": func() []byte { b := nlmsg(0x18, testSeq, nil); binary.NativeEndian.PutUint32(b[0:4], 8); return b }(),
		"attr len below hdr":   nlmsg(0x18, testSeq, []byte{2, 1, 0, 0, 2, 0, 5, 0}),
		"attr len past end":    nlmsg(0x18, testSeq, []byte{2, 1, 0, 0, 99, 0, 5, 0}),
		"error msg no errno":   nlmsg(2, testSeq, []byte{1}),
		"genl header short":    nlmsg(0x18, testSeq, []byte{2, 1}),
	}
	for name, b := range bad {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseTaskstatsReply(b, testSeq); !errors.Is(err, errTaskstatsShort) {
				t.Fatalf("err = %v, want errTaskstatsShort", err)
			}
		})
	}
}

func TestIsUnsupported(t *testing.T) {
	if !IsUnsupported(errTaskstatsUnsupported) || !IsUnsupported(errors.ErrUnsupported) {
		t.Fatal("expected unsupported")
	}
	if IsUnsupported(errTaskstatsShort) || IsUnsupported(nil) {
		t.Fatal("unexpected unsupported")
	}
}
