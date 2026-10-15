package tunnel

import (
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type matchProxy struct{ C.Proxy }

func (matchProxy) Name() string                     { return "DIRECT" }
func (matchProxy) Type() C.AdapterType              { return C.Direct }
func (matchProxy) SupportUDP() bool                 { return true }
func (matchProxy) Unwrap(*C.Metadata, bool) C.Proxy { return nil }

// lockProbeRule checks, while being matched, whether a config writer can take
// configMux: a rule may block on DNS or process lookup here.
type lockProbeRule struct {
	C.Rule
	writerGotLock bool
}

func (r *lockProbeRule) Match(*C.Metadata, C.RuleMatchHelper) (bool, string) {
	done := make(chan struct{})
	go func() {
		configMux.Lock()
		configMux.Unlock()
		close(done)
	}()
	select {
	case <-done:
		r.writerGotLock = true
	case <-time.After(time.Second):
	}
	return true, "DIRECT"
}

// Rule matching must not hold configMux (T-010).
func TestMatchDoesNotHoldConfigLock(t *testing.T) {
	probe := &lockProbeRule{}
	oldRules, oldProxies := rules, proxies
	defer func() { rules, proxies = oldRules, oldProxies }()
	rules, proxies = []C.Rule{probe}, map[string]C.Proxy{"DIRECT": matchProxy{}}

	proxy, rule, err := match(&C.Metadata{NetWork: C.TCP}, C.RuleMatchHelper{})
	if err != nil || proxy == nil || rule != probe {
		t.Fatalf("match = %v, %v, %v", proxy, rule, err)
	}
	if !probe.writerGotLock {
		t.Fatal("a config writer was blocked while a rule was being matched")
	}
}
