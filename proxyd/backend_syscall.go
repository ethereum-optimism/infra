//go:build !windows

package proxyd

import "syscall"

func setSockOptLinger(fd int) error {
	// Unix only
	return syscall.SetsockoptLinger(fd, syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{Onoff: 1, Linger: 1})
}
