//go:build !windows

package proxyd

import "syscall"

func setSockOptLinger(fd int, level int, opt int, l *syscall.Linger) error {
	return syscall.SetsockoptLinger(fd, level, opt, l)
}
