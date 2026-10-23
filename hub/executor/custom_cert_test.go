package executor

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/ca"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
)

// An http proxy over TLS builds its TLS config when it is created, during parsing: a server trusted
// only through tls.custom-certifactes must pass verification (T-115; hysteria2, tuic and socks5 over
// TLS do the same).
func TestCustomTrustCertReachesProxiesBuiltAtParse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // the CONNECT is accepted; only the TLS handshake matters here
	}))
	defer srv.Close()
	t.Cleanup(ca.ResetCertificate)
	ca.ResetCertificate()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	yaml := "proxies:\n  - {name: p, type: http, server: " + host + ", port: " + port + ", tls: true}\n" +
		"tls:\n  custom-certifactes: [" + strconv.Quote(certPEM) + "]\n"
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := cfg.Proxies["p"].DialContext(ctx, &C.Metadata{NetWork: C.TCP, Host: "example.com", DstPort: 80})
	if err != nil && strings.Contains(err.Error(), "x509") {
		t.Fatalf("the custom certificate is not trusted by a proxy built at parse: %v", err)
	}
	if conn != nil {
		_ = conn.Close()
	}
}
