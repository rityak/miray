package inbound_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"

	"github.com/metacubex/sing/common/bufio"
)

// T-103: the miray XHTTP client against an Xray server (XRAY_BIN, e.g. built from
// tools/refs/xray-core): what the client wrote before Close reaches the destination, in
// every mode. Written as the relay writes it, in two parts, then closed at once.
func TestXrayXHTTPUploadThenClose(t *testing.T) {
	xray := os.Getenv("XRAY_BIN")
	if xray == "" {
		t.Skip("XRAY_BIN is not set")
	}
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	_ = os.WriteFile(crt, []byte(tlsCertificate), 0o600)
	_ = os.WriteFile(key, []byte(tlsPrivateKey), 0o600)

	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	got := make(chan int64, 16)
	go func() {
		for {
			c, err := sink.Accept()
			if err != nil {
				return
			}
			go func() { n, _ := io.Copy(io.Discard, c); c.Close(); got <- n }()
		}
	}()
	sinkAddr := sink.Addr().(*net.TCPAddr).AddrPort()

	for _, mode := range []string{"packet-up", "stream-up", "stream-one"} {
		t.Run(mode, func(t *testing.T) {
			port := xrayFreePort(t)
			cfg := map[string]any{
				"log": map[string]any{"loglevel": "warning"},
				"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "vless",
					"settings": map[string]any{"clients": []any{map[string]any{"id": userUUID}}, "decryption": "none"},
					"streamSettings": map[string]any{"network": "xhttp", "security": "tls",
						"tlsSettings":   map[string]any{"certificates": []any{map[string]any{"certificateFile": crt, "keyFile": key}}, "alpn": []string{"h2", "http/1.1"}},
						"xhttpSettings": map[string]any{"path": "/x", "mode": mode}}}},
				"outbounds": []any{map[string]any{"protocol": "freedom"}},
			}
			b, _ := json.Marshal(cfg)
			cfgPath := filepath.Join(dir, mode+".json")
			_ = os.WriteFile(cfgPath, b, 0o600)
			cmd := exec.Command(xray, "run", "-c", cfgPath)
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			waitListening(t, port)

			tunnel := NewHttpTestTunnel()
			defer tunnel.Close()
			opts := outbound.VlessOption{
				Name: "xray_xhttp", Server: "127.0.0.1", Port: port, UUID: userUUID,
				TLS: true, Fingerprint: tlsFingerprint, ServerName: "example.org", ClientFingerprint: "chrome", Network: "xhttp",
				XHTTPOpts: outbound.XHTTPOptions{Path: "/x", Mode: mode},
			}
			opts.DialerForAPI, opts.TunnelForAPI = tunnel.NewDialer(), tunnel
			out, err := outbound.NewVless(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			const size = 32 << 10
			for i := 0; i < 20; i++ {
				conn, err := out.DialContext(context.Background(), &C.Metadata{NetWork: C.TCP, DstIP: sinkAddr.Addr(), DstPort: sinkAddr.Port()})
				if err != nil {
					t.Fatal(err)
				}
				data := make([]byte, size)
				if _, err := conn.Write(data[:8192]); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Write(data[8192:]); err != nil {
					t.Fatal(err)
				}
				conn.Close()
				select {
				case n := <-got:
					if n != size {
						t.Fatalf("transfer %d: the destination got %d of %d bytes written before Close", i, n, size)
					}
				case <-time.After(15 * time.Second):
					t.Fatalf("transfer %d: the destination did not see the end of the upload", i)
				}
			}
		})
	}
}

func xrayFreePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, port int) {
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("xray did not listen on %d", port)
}

// Through VLESS Vision to an Xray server, a short reply the destination sends and
// closes at once reaches the client before the EOF.
func TestXrayVisionReplyThenClose(t *testing.T) {
	xray := os.Getenv("XRAY_BIN")
	if xray == "" {
		t.Skip("XRAY_BIN is not set")
	}
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	_ = os.WriteFile(crt, []byte(tlsCertificate), 0o600)
	_ = os.WriteFile(key, []byte(tlsPrivateKey), 0o600)
	reply := make([]byte, 85)
	for i := range reply {
		reply[i] = byte(i)
	}
	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	go func() {
		for {
			c, err := sink.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.ReadFull(c, make([]byte, 64))
				_, _ = c.Write(reply)
				c.Close()
			}()
		}
	}()
	sinkAddr := sink.Addr().(*net.TCPAddr).AddrPort()
	port := xrayFreePort(t)
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"clients": []any{map[string]any{"id": userUUID, "flow": "xtls-rprx-vision"}}, "decryption": "none"},
			"streamSettings": map[string]any{"network": "raw", "security": "tls",
				"tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": crt, "keyFile": key}}}}}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
	b, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(dir, "vision.json")
	_ = os.WriteFile(cfgPath, b, 0o600)
	cmd := exec.Command(xray, "run", "-c", cfgPath)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitListening(t, port)

	tunnel := NewHttpTestTunnel()
	defer tunnel.Close()
	opts := outbound.VlessOption{Name: "xray_vision", Server: "127.0.0.1", Port: port, UUID: userUUID, Flow: "xtls-rprx-vision",
		TLS: true, Fingerprint: tlsFingerprint, ServerName: "example.org", ClientFingerprint: "chrome"}
	opts.DialerForAPI, opts.TunnelForAPI = tunnel.NewDialer(), tunnel
	out, err := outbound.NewVless(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for i := 0; i < 20; i++ {
		conn, err := out.DialContext(context.Background(), &C.Metadata{NetWork: C.TCP, DstIP: sinkAddr.Addr(), DstPort: sinkAddr.Port()})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		_, err = bufio.Copy(&got, conn) // as the relay copies, through ReadBuffer
		conn.Close()
		if got.Len() != len(reply) {
			t.Fatalf("exchange %d: copied %d of %d reply bytes (err %v)", i, got.Len(), len(reply), err)
		}
	}
}
