//go:build with_gvisor

package sing_tun

import tun "github.com/metacubex/sing-tun"

func closeFailedStack(stack tun.Stack) {
	_ = stack.Close()
	if mixed, ok := stack.(*tun.Mixed); ok {
		// shortcut: sing-tun v0.4.27 skips System.Close on failed Start; remove after dependency cleanup is fixed.
		_ = mixed.System.Close()
	}
}
