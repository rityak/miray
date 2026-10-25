//go:build miraydiag && !windows

package diag

import "syscall"

func errnoClass(e syscall.Errno) string {
	switch e {
	case syscall.ETIMEDOUT:
		return "timeout"
	case syscall.ECONNREFUSED:
		return "refused"
	case syscall.ECONNRESET:
		return "reset"
	case syscall.ECONNABORTED, syscall.EPIPE:
		return "aborted"
	case syscall.ENETUNREACH, syscall.EHOSTUNREACH:
		return "unreachable"
	}
	return ""
}
