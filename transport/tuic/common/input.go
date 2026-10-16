package common

import (
	"net"
	"sync"
)

// inputQueue bounds the datagrams waiting for a slow reader of one association.
const inputQueue = 128

// PacketInput hands the datagrams of one UDP association to its reader (one end
// of a pipe) in arrival order without blocking the QUIC receive loop: a full
// queue drops, like a socket buffer. Before it, every datagram got its own
// goroutine, which reordered a flow and added scheduling delay (T-064).
type PacketInput struct {
	net.Conn
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

func NewPacketInput(conn net.Conn) *PacketInput {
	p := &PacketInput{Conn: conn, ch: make(chan []byte, inputQueue), done: make(chan struct{})}
	go p.run()
	return p
}

// Write queues b, which must not be modified afterwards.
func (p *PacketInput) Write(b []byte) (int, error) {
	select {
	case p.ch <- b:
	default:
	}
	return len(b), nil
}

func (p *PacketInput) Close() error {
	p.once.Do(func() { close(p.done) })
	return p.Conn.Close()
}

func (p *PacketInput) run() {
	for {
		select {
		case b := <-p.ch:
			if _, err := p.Conn.Write(b); err != nil {
				return
			}
		case <-p.done:
			return
		}
	}
}
