//go:build linux

package tunnel

import C "github.com/metacubex/mihomo/constant"

// HasBatchWriter exposes the batch-writer decision to the external test package.
func HasBatchWriter(pc C.PacketConn) bool { return newBatchWriter(pc) != nil }
