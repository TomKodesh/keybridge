package utils

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modiphlpapi             = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afInet                = 2 // AF_INET
	tcpTableOwnerPidAll   = 5 // TCP_TABLE_OWNER_PID_ALL
	errInsufficientBuffer = 122
)

// mibTcpRowOwnerPid mirrors MIB_TCPROW_OWNER_PID.
type mibTcpRowOwnerPid struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPid  uint32
}

// swapPort undoes the network-byte-order packing GetExtendedTcpTable uses
// for the low 16 bits of dwLocalPort/dwRemotePort.
func swapPort(p uint32) uint16 {
	return uint16(p>>8&0xff | p<<8&0xff00)
}

// tcpConnOwnerPID looks up which local process owns the TCP endpoint at
// localPort via the system's TCP connection table. Sockets carry no
// security descriptor to query the way file/process handles do (unlike
// GetHandleSID's use for the Pageant file mapping), so ownership has to be
// resolved this way instead.
func tcpConnOwnerPID(localPort uint16) (uint32, error) {
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(afInet), uintptr(tcpTableOwnerPidAll), 0)
	if size == 0 {
		return 0, fmt.Errorf("GetExtendedTcpTable: could not determine buffer size")
	}

	var buf []byte
	succeeded := false
	for attempt := 0; attempt < 3 && !succeeded; attempt++ {
		buf = make([]byte, size)
		r0, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
			0, uintptr(afInet), uintptr(tcpTableOwnerPidAll), 0,
		)
		switch r0 {
		case 0:
			succeeded = true
		case errInsufficientBuffer:
			continue // size was updated in place; retry with the new size
		default:
			return 0, fmt.Errorf("GetExtendedTcpTable failed: %d", r0)
		}
	}
	if !succeeded {
		return 0, fmt.Errorf("GetExtendedTcpTable: table kept growing, giving up")
	}

	if len(buf) < 4 {
		return 0, fmt.Errorf("GetExtendedTcpTable: short buffer")
	}
	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const rowSize = uint32(unsafe.Sizeof(mibTcpRowOwnerPid{}))
	for i := uint32(0); i < numEntries; i++ {
		off := 4 + i*rowSize
		if off+rowSize > uint32(len(buf)) {
			break
		}
		row := (*mibTcpRowOwnerPid)(unsafe.Pointer(&buf[off]))
		if swapPort(row.LocalPort) == localPort {
			return row.OwningPid, nil
		}
	}
	return 0, fmt.Errorf("no TCP table entry for local port %d", localPort)
}

// SameUserTCPConn reports whether the process on the other end of a loopback
// TCP connection is running as the same Windows user as this process. It
// approximates the access control a Unix-domain socket's filesystem
// permissions would normally provide, for the loopback-TCP fallback path
// used when native AF_UNIX sockets aren't available (see app.WSL.Run).
func SameUserTCPConn(conn net.Conn) (bool, error) {
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return false, fmt.Errorf("SameUserTCPConn: not a TCP connection")
	}

	// For a loopback connection, our peer's remote address (as seen from
	// the accepting side) is that peer's own local endpoint, so it's what
	// shows up as a LocalPort entry in the system TCP table.
	pid, err := tcpConnOwnerPID(uint16(remote.Port))
	if err != nil {
		return false, err
	}

	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(proc)

	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()

	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}

	ourSID, err := GetUserSID()
	if err != nil {
		return false, err
	}

	return windows.EqualSid(tokenUser.User.Sid, ourSID), nil
}
