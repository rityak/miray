package vision

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	N "github.com/metacubex/mihomo/common/net"

	"github.com/metacubex/sing/common/bufio"
)

// eofReader returns its last bytes together with io.EOF, as crypto/tls does when a
// close_notify follows them.
type eofReader struct{ r *bytes.Reader }

func (e eofReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == nil && e.r.Len() == 0 {
		err = io.EOF
	}
	return n, err
}

// T-104: a short reply the server sends and closes at once (padded, then close_notify)
// reaches the copy loop: the data is not dropped together with the EOF.
func TestReadBufferKeepsDataBeforeEOF(t *testing.T) {
	payload := bytes.Repeat([]byte{'r'}, 85)
	frame := []byte{commandPaddingEnd, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(frame[1:], uint16(len(payload)))
	src := eofReader{bytes.NewReader(append(frame, payload...))}
	vc := &Conn{ExtendedReader: N.NewExtendedReader(src)}
	vc.readProcess.Store(true)
	vc.readLastCommand.Store(uint32(commandPaddingContinue))

	var got bytes.Buffer
	if _, err := bufio.Copy(&got, vc); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("copied %d bytes, want %d", got.Len(), len(payload))
	}
}
