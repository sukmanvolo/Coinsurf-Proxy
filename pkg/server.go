package pkg

import (
	"context"
	"errors"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-peerstore/pstoremem"
	rcmgr "github.com/libp2p/go-libp2p-resource-manager"
	connmgr2 "github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	tls "github.com/libp2p/go-libp2p/p2p/security/tls"
	quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
	"strconv"
	"time"
)

const (
	httpProxyAddr      = "0.0.0.0:9091"
	quicMultiaddr      = "/ip4/0.0.0.0/udp/4242/quic"
	tcpMultiaddr       = "/ip4/0.0.0.0/tcp/4242"
	bufferSizeTCP      = 4 * 1024
	bufferSizeUDP      = 65535
	timeoutUDP         = time.Second * 15
	sessionDuration    = time.Minute * 10
	sessionDurationMax = time.Minute * 30
	dialTimeout        = time.Second * 5
)

type Server struct {
	proxyAddrHTTP   string
	proxyAddrSOCKS5 string
	protocol        string
	ip              string

	bufferSizeTCP   int
	bufferSizeUDP   int
	timeoutUDP      time.Duration
	dialPeerTries   int
	timeoutTCP      time.Duration
	sessionDuration time.Duration
	dialTimeout     time.Duration
	developmentIP   string

	host       host.Host
	auth       Auth
	accountant Accountant
	router     Router
	device     DeviceRegistry
	resolver   Resolver
	username   *UsernameParser

	logger *zap.Logger
}

func NewServer(
	auth Auth,
	accountant Accountant,
	router Router,
	device DeviceRegistry,
	options ...Option) (*Server, error) {
	s := &Server{
		auth:          auth,
		accountant:    accountant,
		router:        router,
		device:        device,
		proxyAddrHTTP: httpProxyAddr,
	}

	var err error
	var opt = new(Options)
	if len(options) == 0 {
		return nil, errors.New("provide at least one option")
	}

	for _, o := range options {
		o(opt)
	}

	s.logger, _ = zap.NewProductionConfig().Build()
	if opt.Logger != nil {
		s.logger = opt.Logger
	}

	logging.SetAllLoggers(logging.LevelWarn)

	if opt.TimeoutTCP > 0 {
		s.timeoutTCP = opt.TimeoutTCP
	}

	if opt.ProxyPortHTTP != 0 {
		s.proxyAddrHTTP = ":" + strconv.Itoa(opt.ProxyPortHTTP)
	}

	if opt.ProxyPortSOCKS5 != 0 {
		s.proxyAddrSOCKS5 = ":" + strconv.Itoa(opt.ProxyPortSOCKS5)
	}

	s.bufferSizeTCP = bufferSizeTCP
	if opt.BufferSizeTCP > 0 {
		s.bufferSizeTCP = opt.BufferSizeTCP
	}

	s.bufferSizeUDP = bufferSizeUDP
	if opt.BufferSizeUDP > 0 {
		s.bufferSizeUDP = opt.BufferSizeUDP
	}

	sessionDurationDefault := sessionDuration
	if opt.SessionDuration > 0 {
		sessionDurationDefault = opt.SessionDuration
	}

	sessionDurationMaxDefault := sessionDurationMax
	if opt.SessionDurationMax > 0 {
		sessionDurationMaxDefault = opt.SessionDurationMax
	}

	s.dialTimeout = dialTimeout
	if opt.DialTimeout > 0 {
		s.dialTimeout = opt.DialTimeout
	}

	s.timeoutUDP = timeoutUDP
	if opt.TimeoutUDP > 0 {
		s.timeoutUDP = opt.TimeoutUDP
	}

	if opt.DevelopmentIP != "" {
		s.developmentIP = opt.DevelopmentIP
	}

	if opt.IP != "" {
		s.ip = opt.IP
	}

	s.resolver = opt.Resolver
	s.username = NewUsernameParser(sessionDurationDefault, sessionDurationMaxDefault, opt.LocationLookup)

	var connManager = opt.ConnManager
	if opt.ConnManager == nil {
		connManager, err = connmgr2.NewConnManager(1024*2, 1024*10)
		if err != nil {
			return nil, err
		}
	}

	var resourceManager = opt.ResourceManager
	if opt.ResourceManager == nil {
		defaults := rcmgr.DefaultLimits
		defaults.SystemBaseLimit = rcmgr.BaseLimit{
			ConnsInbound:    1024 * 8,
			ConnsOutbound:   1024 * 8,
			Conns:           1024 * 8,
			StreamsInbound:  1024 * 16,
			StreamsOutbound: 1024 * 32,
			Streams:         1024 * 32,
			Memory:          5 << 30,
			FD:              1024 * 10,
		}

		defaults.ServicePeerBaseLimit = rcmgr.BaseLimit{
			ConnsInbound:    4096 * 2,
			ConnsOutbound:   4096 * 2,
			Conns:           2048 * 4,
			StreamsInbound:  1024 * 16,
			StreamsOutbound: 1024 * 32,
			Streams:         1024 * 64,
			Memory:          1 << 30,
			FD:              1024 * 10,
		}

		defaults.ProtocolBaseLimit = rcmgr.BaseLimit{
			StreamsInbound:  1024 * 16,
			StreamsOutbound: 2048 * 32,
			Streams:         2048 * 32,
			Memory:          1 << 30,
		}

		defaults.TransientBaseLimit = rcmgr.BaseLimit{
			ConnsInbound:    512 * 8,
			ConnsOutbound:   1024 * 8,
			Conns:           1024 * 8,
			StreamsInbound:  1024 * 8,
			StreamsOutbound: 1024 * 8,
			Streams:         2048 * 8,
			Memory:          3 << 30,
			FD:              1024 * 4,
		}

		limiter := rcmgr.NewFixedLimiter(defaults.Scale(5<<30, 1024*10))

		resourceManager, err = rcmgr.NewResourceManager(limiter)
		if err != nil {
			return nil, err
		}
	}

	var addrs = opt.MultiAddrs
	if len(opt.MultiAddrs) == 0 {
		addrs = []multiaddr.Multiaddr{
			multiaddr.StringCast(quicMultiaddr),
			multiaddr.StringCast(tcpMultiaddr),
		}
	}

	var peers = opt.Peerstore
	if opt.Peerstore != nil {
		peers, err = pstoremem.NewPeerstore()
		if err != nil {
			return nil, err
		}
	}

	var key ic.PrivKey
	if opt.PrivKey != nil {
		key = opt.PrivKey
	}

	s.host, err = libp2p.New(
		libp2p.ListenAddrs(addrs...),
		libp2p.Identity(key),
		libp2p.Security(tls.ID, tls.New),
		libp2p.Security(noise.ID, noise.New),
		libp2p.ForceReachabilityPublic(),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.ConnectionManager(connManager),
		//libp2p.EnableNATService(),
		libp2p.Ping(true),
		libp2p.ResourceManager(resourceManager),
		libp2p.Transport(quic.NewTransport),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Peerstore(peers))
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Server) Listen(ctx context.Context) error {
	authSvc := NewServiceAuth(
		"server.auth",
		s.developmentIP,
		s.host,
		s.auth,
		s.device,
		s.router,
		s.resolver,
		s.logger)
	go authSvc.Serve(ctx)

	transfer := NewTransfer(s.bufferSizeTCP, s.timeoutTCP, s.accountant)

	dialer := NewDialer(s.dialTimeout, s.router, s.host)

	httpProxySvc := NewServiceProxyHTTP(
		"server.http_proxy",
		s.proxyAddrHTTP,
		transfer,
		s.username,
		s.auth,
		s.accountant,
		s.router,
		dialer,
		s.logger)
	go httpProxySvc.Serve(ctx)

	socks5ProxySvc, err := NewServiceProxySOCKS5(
		"server.socks5_proxy",
		s.proxyAddrSOCKS5,
		s.ip,
		s.bufferSizeUDP,
		s.timeoutUDP,
		transfer,
		s.username,
		s.auth,
		s.accountant,
		s.router,
		dialer,
		s.logger)
	if err != nil {
		return err
	}
	go socks5ProxySvc.Serve(ctx)

	<-ctx.Done()
	err = s.host.Close()
	if err != nil {
		s.logger.Error("Failed to gracefully stop Host", zap.Error(err))
	}

	return nil
}
