package runtimemetrics

import (
	"encoding/binary"
	"fmt"
	"math"
	"syscall"
	"time"
)

// Constants below are defined locally to avoid extra imports and to keep this
// file buildable on every platform.
const (
	// linux/netlink.h
	nlmsgHdrLen = 16 // sizeof(struct nlmsghdr)
	nlmsgError  = 2  // NLMSG_ERROR
	nlaHdrLen   = 4  // sizeof(struct nlattr)
	nlAlign     = 4  // NLMSG_ALIGNTO and NLA_ALIGNTO
	// nlaTypeMask clears NLA_F_NESTED (1<<15) and NLA_F_NET_BYTEORDER (1<<14).
	nlaTypeMask = 0x3fff

	// linux/genetlink.h
	genlHdrLen = 4 // sizeof(struct genlmsghdr)

	// linux/taskstats.h, enum TASKSTATS_TYPE_*
	taskstatsTypeStats    = 3 // TASKSTATS_TYPE_STATS
	taskstatsTypeAggrTGID = 5 // TASKSTATS_TYPE_AGGR_TGID
	// TASKSTATS_TYPE_NULL (6) and every other type is skipped by default.

	// Byte offsets of the delay totals inside struct taskstats (linux/taskstats.h).
	// Every u64 is 8-aligned; offsets match unsafe.Offsetof on x/sys/unix.Taskstats.
	offVersion        = 0   // u16 version
	offCPUDelay       = 24  // cpu_delay_total (version 1)
	offBlkioDelay     = 40  // blkio_delay_total (version 1)
	offSwapinDelay    = 56  // swapin_delay_total (version 1)
	offFreepagesDelay = 320 // freepages_delay_total (version >= 7)

	// minVersionFreepages is the first taskstats version with freepages_delay_total.
	minVersionFreepages = 7
)

// align4 rounds n up to the next multiple of 4 (NLMSG_ALIGN / NLA_ALIGN).
func align4(n int) int { return (n + nlAlign - 1) &^ (nlAlign - 1) }

// ParseTaskstatsReply scans buf, a buffer of netlink messages, for the
// taskstats reply to the request with sequence number seq. Messages with a
// different sequence are ignored. It returns found=false with a nil error if
// no matching data message was present. It never panics on malformed input.
func ParseTaskstatsReply(buf []byte, seq uint32) (DelayInfo, bool, error) {
	for len(buf) > 0 {
		// Decode the nlmsghdr: len u32, type u16, flags u16, seq u32, pid u32.
		if len(buf) < nlmsgHdrLen {
			return DelayInfo{}, false, fmt.Errorf("%w: netlink header", errTaskstatsShort)
		}
		msgLen := int(binary.NativeEndian.Uint32(buf[0:4]))
		msgType := binary.NativeEndian.Uint16(buf[4:6])
		msgSeq := binary.NativeEndian.Uint32(buf[8:12])
		if msgLen < nlmsgHdrLen || msgLen > len(buf) {
			return DelayInfo{}, false, fmt.Errorf("%w: netlink length %d of %d", errTaskstatsShort, msgLen, len(buf))
		}
		payload := buf[nlmsgHdrLen:msgLen]
		// Advance to the next message; the final one may lack padding.
		next := min(align4(msgLen), len(buf))
		buf = buf[next:]

		// Ignore replies that belong to another request.
		if msgSeq != seq {
			continue
		}

		if msgType == nlmsgError {
			// Payload starts with int32 errno (negative); 0 is an ACK.
			if len(payload) < 4 {
				return DelayInfo{}, false, fmt.Errorf("%w: netlink error payload", errTaskstatsShort)
			}
			code := binary.NativeEndian.Uint32(payload[0:4])
			if code == 0 {
				continue
			}
			// Negate the two's-complement value without signed conversions.
			if code&(1<<31) != 0 {
				code = -code
			}
			return DelayInfo{}, false, fmt.Errorf("taskstats: netlink error: %w", syscall.Errno(code))
		}

		info, ok, err := parseTaskstatsMessage(payload)
		if err != nil || ok {
			return info, ok, err
		}
	}
	return DelayInfo{}, false, nil
}

// parseTaskstatsMessage decodes one data message payload (genlmsghdr followed
// by attributes). ok is false when the message carries no stats.
func parseTaskstatsMessage(payload []byte) (DelayInfo, bool, error) {
	// Skip the 4-byte genlmsghdr (cmd, version, reserved).
	if len(payload) < genlHdrLen {
		return DelayInfo{}, false, fmt.Errorf("%w: genetlink header", errTaskstatsShort)
	}
	// Find the AGGR_TGID nest among the top-level attributes.
	aggr, ok, err := findAttr(payload[genlHdrLen:], taskstatsTypeAggrTGID)
	if err != nil || !ok {
		return DelayInfo{}, false, err
	}
	// Inside the nest, find the Taskstats struct (skipping TGID, NULL pad, ...).
	stats, ok, err := findAttr(aggr, taskstatsTypeStats)
	if err != nil || !ok {
		return DelayInfo{}, false, err
	}
	info, err := decodeTaskstats(stats)
	if err != nil {
		return DelayInfo{}, false, err
	}
	return info, true, nil
}

// findAttr walks a netlink attribute list and returns the value of the first
// attribute of type want. Other attributes (including TASKSTATS_TYPE_NULL
// padding) are skipped using their NLA_ALIGN-padded length.
func findAttr(buf []byte, want uint16) (value []byte, ok bool, err error) {
	for len(buf) > 0 {
		// Decode nlattr: len u16 (header included), type u16.
		if len(buf) < nlaHdrLen {
			return nil, false, fmt.Errorf("%w: attribute header", errTaskstatsShort)
		}
		attrLen := int(binary.NativeEndian.Uint16(buf[0:2]))
		attrType := binary.NativeEndian.Uint16(buf[2:4]) & nlaTypeMask
		if attrLen < nlaHdrLen || attrLen > len(buf) {
			return nil, false, fmt.Errorf("%w: attribute length %d of %d", errTaskstatsShort, attrLen, len(buf))
		}
		if attrType == want {
			return buf[nlaHdrLen:attrLen], true, nil
		}
		// Values are padded to NLA_ALIGN; the last one may lack padding.
		buf = buf[min(align4(attrLen), len(buf)):]
	}
	return nil, false, nil
}

// decodeTaskstats reads the delay totals from a struct taskstats payload,
// using the Version field and payload length to decide which exist.
func decodeTaskstats(p []byte) (DelayInfo, error) {
	// Version-1 fields end at swapin_delay_total (offset 56 + 8).
	if len(p) < offSwapinDelay+8 {
		return DelayInfo{}, fmt.Errorf("%w: taskstats payload %d bytes", errTaskstatsShort, len(p))
	}
	version := binary.NativeEndian.Uint16(p[offVersion : offVersion+2])
	info := DelayInfo{
		CPU:     nsToDuration(binary.NativeEndian.Uint64(p[offCPUDelay:])),
		BlockIO: nsToDuration(binary.NativeEndian.Uint64(p[offBlkioDelay:])),
		SwapIn:  nsToDuration(binary.NativeEndian.Uint64(p[offSwapinDelay:])),
	}
	if version >= minVersionFreepages {
		// The kernel claims freepages exists, so a shorter payload is malformed.
		if len(p) < offFreepagesDelay+8 {
			return DelayInfo{}, fmt.Errorf("%w: taskstats v%d payload %d bytes", errTaskstatsShort, version, len(p))
		}
		info.FreePages = nsToDuration(binary.NativeEndian.Uint64(p[offFreepagesDelay:]))
	}
	return info, nil
}

// nsToDuration converts nanoseconds to a Duration, saturating at MaxInt64.
func nsToDuration(ns uint64) time.Duration {
	if ns > math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(int64(ns))
}
