package pkg

import (
	"context"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"time"
)

type Dialer struct {
	dialTimeout time.Duration
	router      Router
	host        host.Host
}

func NewDialer(dialTimeout time.Duration, router Router, host host.Host) *Dialer {
	return &Dialer{dialTimeout: dialTimeout, router: router, host: host}
}

func (d *Dialer) Dial(request *Request) (network.Stream, error) {
	var cancel context.CancelFunc
	ctx := context.Background()
	if d.dialTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, d.dialTimeout)
		defer cancel()
	}

	protocol := ProxyHTTPProtocol
	if request.SOCKS5 {
		protocol = ProxySOCKS5Protocol
	}

	return d.host.NewStream(ctx, request.PeerID, protocol)
}

func (d *Dialer) Disconnect(id peer.ID) error {
	_ = d.host.Network().ClosePeer(id)
	d.host.Peerstore().RemovePeer(id)
	return d.router.Remove(id)
}
