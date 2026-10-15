//go:build !with_gvisor

package sing_tun

import tun "github.com/metacubex/sing-tun"

func closeFailedStack(stack tun.Stack) {
	_ = stack.Close()
}
