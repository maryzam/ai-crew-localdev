package broker

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func PeerCred(conn *net.UnixConn) (uid, gid, pid uint32, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("peercred: syscall conn: %w", err)
	}

	var ucred *unix.Ucred
	var credErr error

	err = raw.Control(func(fd uintptr) {
		ucred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("peercred: control: %w", err)
	}
	if credErr != nil {
		return 0, 0, 0, fmt.Errorf("peercred: getsockopt: %w", credErr)
	}

	return uint32(ucred.Uid), uint32(ucred.Gid), uint32(ucred.Pid), nil
}
