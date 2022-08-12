package pkg

import (
	"github.com/libp2p/go-libp2p-core/connmgr"
	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
	"time"
)

type Options struct {
	BufferSizeTCP      int
	BufferSizeUDP      int
	DialTimeout        time.Duration
	SessionDuration    time.Duration
	SessionDurationMax time.Duration
	TimeoutTCP         time.Duration
	TimeoutUDP         time.Duration
	ProxyPortHTTP      int
	ProxyPortSOCKS5    int
	ConnManager        connmgr.ConnManager
	ResourceManager    network.ResourceManager
	IP                 string
	DevelopmentIP      string
	PrivKey            ic.PrivKey
	Logger             *zap.Logger
	LocationLookup     LocationLookup
	Resolver           Resolver
	Peerstore          peerstore.Peerstore
	MultiAddrs         []multiaddr.Multiaddr
}

type Option func(*Options)

//WithDevelopmentIP - is used in testing the application. If the IP address matches the one provided
//it will be spoofed to determine the location
func WithDevelopmentIP(ip string) Option {
	return func(options *Options) {
		options.DevelopmentIP = ip
	}
}

//WithIP - used to resolve IP for SOCKS5 UDP server
func WithIP(ip string) Option {
	return func(options *Options) {
		options.IP = ip
	}
}

//WithTimeoutTCP - used in data copying process for HTTP & SOCKS5
func WithTimeoutTCP(t time.Duration) Option {
	return func(options *Options) {
		options.TimeoutTCP = t
	}
}

//WithTimeoutUDP - used in data copying process for SOCKS5 UDP
func WithTimeoutUDP(t time.Duration) Option {
	return func(options *Options) {
		options.TimeoutUDP = t
	}
}

//WithSessionDuration - default sticky session lifetime
func WithSessionDuration(duration time.Duration) Option {
	return func(options *Options) {
		options.SessionDuration = duration
	}
}

//WithSessionDurationMax - max session lifetime, if user provided value bigger than that
//then default session default lifetime will be applied
func WithSessionDurationMax(duration time.Duration) Option {
	return func(options *Options) {
		options.SessionDurationMax = duration
	}
}

//WithDialTimeout - time to dial exit node
func WithDialTimeout(timeout time.Duration) Option {
	return func(options *Options) {
		options.DialTimeout = timeout
	}
}

func WithProxyPortHTTP(port int) Option {
	return func(options *Options) {
		options.ProxyPortHTTP = port
	}
}

func WithProxyPortSOCKS5(port int) Option {
	return func(options *Options) {
		options.ProxyPortSOCKS5 = port
	}
}

func WithConnManager(cm connmgr.ConnManager) Option {
	return func(options *Options) {
		options.ConnManager = cm
	}
}

func WithResourceManager(rm network.ResourceManager) Option {
	return func(options *Options) {
		options.ResourceManager = rm
	}
}

func WithPrivKey(key ic.PrivKey) Option {
	return func(options *Options) {
		options.PrivKey = key
	}
}

//WithBufferSizeTCP - used in data copying process for HTTP & SOCKS5
func WithBufferSizeTCP(size int) Option {
	return func(options *Options) {
		options.BufferSizeTCP = size
	}
}

//WithBufferSizeUDP - used in data copying process for SOCKS5 UDP
func WithBufferSizeUDP(size int) Option {
	return func(options *Options) {
		options.BufferSizeUDP = size
	}
}

func WithAddrs(addrs ...multiaddr.Multiaddr) Option {
	return func(options *Options) {
		options.MultiAddrs = addrs
	}
}

func WithLogger(logger *zap.Logger) Option {
	return func(options *Options) {
		options.Logger = logger
	}
}

func WithLocationLookup(lookup LocationLookup) Option {
	return func(options *Options) {
		options.LocationLookup = lookup
	}
}

func WithIPResolver(resolver Resolver) Option {
	return func(options *Options) {
		options.Resolver = resolver
	}
}

func WithPeerstore(ps peerstore.Peerstore) Option {
	return func(options *Options) {
		options.Peerstore = ps
	}
}
