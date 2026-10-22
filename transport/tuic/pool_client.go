package tuic

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"

	list "github.com/bahlo/generic-list-go"
)

type PoolClient struct {
	newClientOptionV4 *ClientOptionV4
	newClientOptionV5 *ClientOptionV5

	dialFn DialFunc
	// TCP and UDP share connections, as in sing-box: a first UDP session no longer
	// waits for a handshake of its own while a connection to the server is up (T-093)
	clients      list.List[Client]
	clientsMutex sync.Mutex
}

func (t *PoolClient) DialContext(ctx context.Context, metadata *C.Metadata) (net.Conn, error) {
	conn, err := t.getClient().DialContext(ctx, metadata)
	if errors.Is(err, TooManyOpenStreams) {
		conn, err = t.newClient().DialContext(ctx, metadata)
	}
	if err != nil {
		return nil, err
	}
	return N.NewRefConn(conn, t), err
}

func (t *PoolClient) ListenPacket(ctx context.Context, metadata *C.Metadata) (net.PacketConn, error) {
	pc, err := t.getClient().ListenPacket(ctx, metadata)
	if errors.Is(err, TooManyOpenStreams) {
		pc, err = t.newClient().ListenPacket(ctx, metadata)
	}
	if err != nil {
		return nil, err
	}
	return N.NewRefPacketConn(pc, t), nil
}

func (t *PoolClient) newClient() (client Client) {
	t.clientsMutex.Lock()
	defer t.clientsMutex.Unlock()

	if t.newClientOptionV4 != nil {
		client = NewClientV4(t.newClientOptionV4, true, t.dialFn)
	} else {
		client = NewClientV5(t.newClientOptionV5, true, t.dialFn)
	}

	client.SetLastVisited(time.Now())

	t.clients.PushFront(client)
	return client
}

func (t *PoolClient) getClient() Client {
	clients := &t.clients
	clientsMutex := &t.clientsMutex
	var bestClient Client

	func() {
		clientsMutex.Lock()
		defer clientsMutex.Unlock()
		for it := clients.Front(); it != nil; {
			client := it.Value
			if client == nil {
				next := it.Next()
				clients.Remove(it)
				it = next
				continue
			}
			if bestClient == nil {
				bestClient = client
			} else {
				if client.OpenStreams() < bestClient.OpenStreams() {
					bestClient = client
				}
			}
			it = it.Next()
		}
		for it := clients.Front(); it != nil; {
			client := it.Value
			if client != bestClient && client.OpenStreams() == 0 && time.Now().Sub(client.LastVisited()) > 30*time.Minute {
				client.Close()
				next := it.Next()
				clients.Remove(it)
				it = next
				continue
			}
			it = it.Next()
		}
	}()

	if bestClient == nil {
		return t.newClient()
	} else {
		bestClient.SetLastVisited(time.Now())
		return bestClient
	}
}

func NewPoolClientV4(clientOption *ClientOptionV4, dialFn DialFunc) *PoolClient {
	p := &PoolClient{
		dialFn: dialFn,
	}
	newClientOption := *clientOption
	p.newClientOptionV4 = &newClientOption
	return p
}

func NewPoolClientV5(clientOption *ClientOptionV5, dialFn DialFunc) *PoolClient {
	p := &PoolClient{
		dialFn: dialFn,
	}
	newClientOption := *clientOption
	p.newClientOptionV5 = &newClientOption
	return p
}
