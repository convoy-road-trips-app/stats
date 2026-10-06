package runtimemetrics

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"syscall"
	"testing"
)

func u16(v uint16) []byte { return binary.NativeEndian.AppendUint16(nil, v) }

func u32(v uint32) []byte { return binary.NativeEndian.AppendUint32(nil, v) }

func TestFamilyRequest(t *testing.T) {
	want := slices.Concat(
		// nlmsghdr: length, GENL_ID_CTRL, NLM_F_REQUEST, sequence, port id.
		u32(36), u16(0x10), u16(1), u32(7), u32(0),
		// genlmsghdr: CTRL_CMD_GETFAMILY, version 1, reserved.
		[]byte{3, 1, 0, 0},
		// CTRL_ATTR_FAMILY_NAME: nla_len 14 covers the NUL-terminated name
		// but not the 2 padding bytes; nlmsg_len covers both.
		u16(14), u16(2), []byte("TASKSTATS\x00"), []byte{0, 0},
	)
	if got := familyRequest(7); !bytes.Equal(got, want) {
		t.Fatalf("familyRequest(7) =\n% x\nwant\n% x", got, want)
	}
}

func TestTaskstatsRequest(t *testing.T) {
	want := slices.Concat(
		// nlmsghdr: length, family id, NLM_F_REQUEST, sequence, port id.
		u32(28), u16(0x1b), u16(1), u32(8), u32(0),
		// genlmsghdr: TASKSTATS_CMD_GET, version 1, reserved.
		[]byte{1, 1, 0, 0},
		// TASKSTATS_CMD_ATTR_TGID: u32 pid, already 4-byte aligned.
		u16(8), u16(2), u32(4242),
	)
	if got := taskstatsRequest(0x1b, 8, 4242); !bytes.Equal(got, want) {
		t.Fatalf("taskstatsRequest(0x1b, 8, 4242) =\n% x\nwant\n% x", got, want)
	}
}

// familyReply builds a CTRL_CMD_NEWFAMILY reply with the attributes the
// kernel sends for TASKSTATS, in the kernel's order.
func familyReply(seq uint32, id uint16) []byte {
	op := attr(1, slices.Concat(attr(1, u32(1)), attr(2, u32(1)))) // CTRL_ATTR_OP_ID, CTRL_ATTR_OP_FLAGS
	return nlmsg(0x10, seq, slices.Concat(
		[]byte{1, 2, 0, 0},               // genlmsghdr: CTRL_CMD_NEWFAMILY, version 2
		attr(2, []byte("TASKSTATS\x00")), // CTRL_ATTR_FAMILY_NAME, 2 bytes of padding
		attr(1, u16(id)),                 // CTRL_ATTR_FAMILY_ID, 2 bytes of padding
		attr(3, u32(1)),                  // CTRL_ATTR_VERSION
		attr(4, u32(0)),                  // CTRL_ATTR_HDRSIZE
		attr(5, u32(4)),                  // CTRL_ATTR_MAXATTR
		attr(6|1<<15, op),                // CTRL_ATTR_OPS, NLA_F_NESTED
	))
}

func TestParseFamilyReply(t *testing.T) {
	tests := []struct {
		name            string
		buf             []byte
		wantID          uint16
		wantFound       bool
		wantErr         error
		wantUnsupported bool
	}{
		{name: "valid", buf: familyReply(testSeq, 0x1b), wantID: 0x1b, wantFound: true},
		{name: "empty buffer"},
		{name: "wrong sequence ignored", buf: familyReply(testSeq+1, 0x1b)},
		{
			name:   "wrong sequence then right",
			buf:    slices.Concat(familyReply(testSeq+1, 0x99), familyReply(testSeq, 0x1b)),
			wantID: 0x1b, wantFound: true,
		},
		{
			name:   "ack ignored",
			buf:    slices.Concat(errMsg(testSeq, 0), familyReply(testSeq, 0x1b)),
			wantID: 0x1b, wantFound: true,
		},
		{
			name:   "id after nested ops",
			buf:    nlmsg(0x10, testSeq, slices.Concat([]byte{1, 2, 0, 0}, attr(6|1<<15, attr(1, u32(1))), attr(1, u16(0x1c)))),
			wantID: 0x1c, wantFound: true,
		},
		{
			name:            "ENOENT means no taskstats family",
			buf:             errMsg(testSeq, -int32(syscall.ENOENT)),
			wantErr:         syscall.ENOENT,
			wantUnsupported: true,
		},
		{name: "EPERM", buf: errMsg(testSeq, -int32(syscall.EPERM)), wantErr: syscall.EPERM},
		{name: "no family id", buf: nlmsg(0x10, testSeq, slices.Concat([]byte{1, 2, 0, 0}, attr(2, []byte("TASKSTATS\x00"))))},
		{
			name:    "family id shorter than u16",
			buf:     nlmsg(0x10, testSeq, slices.Concat([]byte{1, 2, 0, 0}, attr(1, []byte{0x1b}))),
			wantErr: errTaskstatsShort,
		},
		{name: "genl header short", buf: nlmsg(0x10, testSeq, []byte{1, 2}), wantErr: errTaskstatsShort},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, found, err := parseFamilyReply(tc.buf, testSeq)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if IsUnsupported(err) != tc.wantUnsupported {
				t.Fatalf("IsUnsupported(%v) = %v, want %v", err, !tc.wantUnsupported, tc.wantUnsupported)
			}
			if id != tc.wantID || found != tc.wantFound {
				t.Fatalf("got (%#x, %v), want (%#x, %v)", id, found, tc.wantID, tc.wantFound)
			}
		})
	}
}

func TestParseFamilyReplyTruncated(t *testing.T) {
	full := familyReply(testSeq, 0x1b)
	// Every strict prefix must return without panicking; a cut header or
	// attribute is reported as errTaskstatsShort.
	for n := range full {
		id, found, err := parseFamilyReply(full[:n], testSeq)
		if found || id != 0 {
			t.Fatalf("prefix %d: got (%#x, %v), want not found", n, id, found)
		}
		if n > 0 && !errors.Is(err, errTaskstatsShort) {
			t.Fatalf("prefix %d: err = %v, want errTaskstatsShort", n, err)
		}
	}
}
