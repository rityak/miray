//go:build miraydiag && windows

package diag

import "syscall"

// Winsock codes: syscall.ECONNREFUSED and the like are not what Windows returns.
func errnoClass(e syscall.Errno) string {
	switch e {
	case 10060: // WSAETIMEDOUT
		return "timeout"
	case 10061: // WSAECONNREFUSED
		return "refused"
	case 10054: // WSAECONNRESET
		return "reset"
	case 10053: // WSAECONNABORTED
		return "aborted"
	case 10051, 10065: // WSAENETUNREACH, WSAEHOSTUNREACH
		return "unreachable"
	}
	return ""
}
