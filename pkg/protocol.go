package pkg

import "github.com/libp2p/go-libp2p-core/protocol"

const (
	ProxyHTTPProtocol     = protocol.ID("/proxy/0.0.1")
	ProxySOCKS5Protocol   = protocol.ID("/proxy-socks5/0.0.1")
	AuthorizationProtocol = protocol.ID("/authorization/0.0.1")
)
