//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "golang.org/x/sys/unix"

// isTerminalFd asks the kernel for the terminal attributes of fd; only a
// terminal has them. /dev/null and pipes do not.
func isTerminalFd(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	return err == nil
}
