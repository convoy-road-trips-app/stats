package runtimemetrics

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
)

// Request-side constants, defined locally like those in taskstats_parse.go.
const (
	// linux/netlink.h
	nlmFRequest = 0x1 // NLM_F_REQUEST

	// linux/genetlink.h: the controller resolves family names to ids.
	genlIDCtrl         = 0x10 // GENL_ID_CTRL
	ctrlVersion        = 1    // genlmsghdr version sent to the controller
	ctrlCmdGetFamily   = 3    // CTRL_CMD_GETFAMILY
	ctrlAttrFamilyID   = 1    // CTRL_ATTR_FAMILY_ID, u16
	ctrlAttrFamilyName = 2    // CTRL_ATTR_FAMILY_NAME, NUL-terminated string

	// linux/taskstats.h
	taskstatsFamilyName  = "TASKSTATS" // TASKSTATS_GENL_NAME
	taskstatsGenlVersion = 1           // TASKSTATS_GENL_VERSION
	taskstatsCmdGet      = 1           // TASKSTATS_CMD_GET
	taskstatsCmdAttrTGID = 2           // TASKSTATS_CMD_ATTR_TGID, u32

	// reqAttrOff is where a request's single attribute starts.
	reqAttrOff = nlmsgHdrLen + genlHdrLen
)

// familyRequest encodes CTRL_CMD_GETFAMILY, asking the controller for the id
// of the TASKSTATS family.
func familyRequest(seq uint32) []byte {
	// The value is zeroed, so copying the name leaves its NUL terminator.
	const nameLen = uint8(len(taskstatsFamilyName) + 1)
	msg, value := genlRequest(genlIDCtrl, seq, ctrlCmdGetFamily, ctrlVersion, ctrlAttrFamilyName, nameLen)
	copy(value, taskstatsFamilyName)
	return msg
}

// taskstatsRequest encodes TASKSTATS_CMD_GET for the thread group tgid, sent
// to the resolved TASKSTATS family id.
func taskstatsRequest(family uint16, seq, tgid uint32) []byte {
	msg, value := genlRequest(family, seq, taskstatsCmdGet, taskstatsGenlVersion, taskstatsCmdAttrTGID, 4)
	binary.NativeEndian.PutUint32(value, tgid)
	return msg
}

// genlRequest allocates a request with NLM_F_REQUEST, a genlmsghdr and one
// attribute, and returns it with the attribute's zeroed valueLen-byte value
// for the caller to fill. nla_len excludes the padding to 4 bytes;
// nlmsg_len includes it. A uint8 valueLen keeps every length in range.
func genlRequest(msgType uint16, seq uint32, cmd, version uint8, attrType uint16, valueLen uint8) (msg, value []byte) {
	attrLen := nlaHdrLen + uint16(valueLen)
	msgLen := reqAttrOff + (uint32(attrLen+nlAlign-1) &^ (nlAlign - 1))

	msg = make([]byte, msgLen)
	// nlmsghdr: len, type, flags, seq; the kernel ignores the port id.
	binary.NativeEndian.PutUint32(msg[0:4], msgLen)
	binary.NativeEndian.PutUint16(msg[4:6], msgType)
	binary.NativeEndian.PutUint16(msg[6:8], nlmFRequest)
	binary.NativeEndian.PutUint32(msg[8:12], seq)
	// genlmsghdr: cmd, version, reserved.
	msg[nlmsgHdrLen] = cmd
	msg[nlmsgHdrLen+1] = version
	// nlattr: len, type, then the value.
	binary.NativeEndian.PutUint16(msg[reqAttrOff:], attrLen)
	binary.NativeEndian.PutUint16(msg[reqAttrOff+2:], attrType)
	return msg, msg[reqAttrOff+nlaHdrLen : reqAttrOff+int(attrLen)]
}

// parseFamilyReply scans buf for the controller's reply to the
// CTRL_CMD_GETFAMILY request seq and returns the TASKSTATS family id, with
// the same sequence and error handling as ParseTaskstatsReply. ENOENT means
// the kernel has no taskstats (CONFIG_TASKSTATS is off, or an older kernel
// hides it from network namespaces), reported as unsupported.
func parseFamilyReply(buf []byte, seq uint32) (id uint16, found bool, err error) {
	id, found, err = findReply(buf, seq, parseFamilyMessage)
	if errors.Is(err, syscall.ENOENT) {
		return 0, false, fmt.Errorf("%w: no %s generic netlink family: %w",
			errTaskstatsUnsupported, taskstatsFamilyName, syscall.ENOENT)
	}
	return id, found, err
}

// parseFamilyMessage reads CTRL_ATTR_FAMILY_ID from one controller reply
// payload (genlmsghdr followed by attributes).
func parseFamilyMessage(payload []byte) (id uint16, ok bool, err error) {
	if len(payload) < genlHdrLen {
		return 0, false, fmt.Errorf("%w: genetlink header", errTaskstatsShort)
	}
	value, ok, err := findAttr(payload[genlHdrLen:], ctrlAttrFamilyID)
	if err != nil || !ok {
		return 0, false, err
	}
	if len(value) < 2 {
		return 0, false, fmt.Errorf("%w: family id %d bytes", errTaskstatsShort, len(value))
	}
	return binary.NativeEndian.Uint16(value), true, nil
}
