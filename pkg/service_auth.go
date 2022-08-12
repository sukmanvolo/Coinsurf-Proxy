package pkg

import (
	"context"
	"github.com/libp2p/go-libp2p-core/event"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-msgio"
	"github.com/mailru/easyjson"
	"github.com/multiformats/go-multiaddr"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"
	"net"
	"strings"
)

type ServiceAuth struct {
	name          string
	developmentIP string
	host          host.Host
	auth          Auth
	device        DeviceRegistry
	router        Router
	resolver      Resolver
	logger        *zap.Logger
}

func NewServiceAuth(name string, developmentIP string, host host.Host, auth Auth, device DeviceRegistry, router Router, resolver Resolver, logger *zap.Logger) *ServiceAuth {
	return &ServiceAuth{name: name, developmentIP: developmentIP, host: host, auth: auth, device: device, router: router, resolver: resolver, logger: logger}
}

var (
	ok     = []byte(`1`)
	failed = []byte(`0`)
)

func (s *ServiceAuth) Serve(ctx context.Context) error {
	s.logger.Info("Starting Authenticate service")
	defer s.logger.Info("Stopping Authenticate service")

	subscriptions := []interface{}{
		new(event.EvtPeerConnectednessChanged),
	}

	subscription, err := s.host.EventBus().Subscribe(subscriptions)
	if err != nil {
		return err
	}

	defer subscription.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-subscription.Out():
			switch e.(type) {
			case event.EvtPeerConnectednessChanged:
				ee := e.(event.EvtPeerConnectednessChanged)

				switch ee.Connectedness {
				case network.Connected:
					go s.OnConnect(ee.Peer)
					break
				case network.NotConnected:
					go s.OnDisconnect(ee.Peer)
					break
				}
			}
			break
		}
	}
}

func unauthorized(stream network.Stream, _ peer.ID) {
	w := msgio.NewWriter(stream)
	w.Write(failed)
	w.Close()
	stream.Close()
}

func disconnect(h host.Host, id peer.ID) {
	h.Peerstore().RemovePeer(id)
	h.Network().ClosePeer(id)
}

func (s *ServiceAuth) OnConnect(pid peer.ID) {
	conns := s.host.Network().ConnsToPeer(pid)
	if len(conns) == 0 {
		disconnect(s.host, pid)
		s.logger.Error("failed to retrieve remote multiaddr",
			zap.String("peer_id", pid.String()))
		return
	}

	//TODO consider checking all available connections
	conn := conns[0]

	var ip, ipv4, ipv6 string
	ipv4, _ = conn.RemoteMultiaddr().ValueForProtocol(multiaddr.P_IP4)
	ipv6, _ = conn.RemoteMultiaddr().ValueForProtocol(multiaddr.P_IP6)
	if ipv4 == "" && ipv6 == "" {
		disconnect(s.host, pid)
		s.logger.Error("failed to determine node ip")
		return
	}

	ip = ipv4
	if ipv6 != "" {
		ip = ipv6
	}

	if s.developmentIP != "" && (strings.HasPrefix(ip, "192.168.0.") || strings.HasPrefix(ip, "127.0.0.")) {
		ip = s.developmentIP
		ipv4 = s.developmentIP
	}

	node, err := s.router.GetByIP(ip)
	if err == nil {
		disconnect(s.host, node.ID)

		err = s.router.Remove(node.ID)
		if err != nil {
			s.logger.Warn("failed to remove duplicate node",
				zap.Error(err),
				zap.String("ip", ip))
			return
		}

		s.logger.Info("peer replaced", zap.String("ip", ip))
	}

	stream, err := s.host.NewStream(context.Background(), pid, AuthorizationProtocol)
	if err != nil {
		s.logger.Error("unauthorized", zap.String("ip", ip), zap.Error(err))
		disconnect(s.host, pid)
		return
	}

	if err = stream.Scope().SetService(s.name); err != nil {
		s.logger.Error("failed to set service",
			zap.String("service", s.name),
			zap.String("ip", ip),
			zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	var token = make([]byte, 512)
	rw := msgio.NewReadWriter(stream)
	read, err := rw.Read(token)
	if err != nil {
		s.logger.Error("failed to read first message", zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	var msg authMessage
	err = easyjson.Unmarshal(token[:read], &msg)
	if err != nil {
		s.logger.Error("failed to unmarshal auth message", zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	if len(msg.UserAgent) == 0 {
		s.logger.Error("user agent is empty", zap.String("api_key", msg.APIKey))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	uaParts := strings.Split(msg.UserAgent, "#")
	if len(uaParts) != 3 {
		s.logger.Error("invalid user agent",
			zap.String("user_agent", msg.UserAgent),
			zap.String("api_key", msg.APIKey))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	_, err = uuid.FromString(msg.APIKey)
	if err != nil {
		s.logger.Error("failed to format uuid", zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	nodeId, _, err := s.auth.Authenticate(context.Background(), []byte(msg.APIKey))
	if err != nil {
		s.logger.Warn("failed to authorize client", zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	_, err = rw.Write(ok)
	if err != nil {
		s.logger.Warn("failed to write authorization response", zap.Error(err))
		unauthorized(stream, pid)
		disconnect(s.host, pid)
		return
	}

	err = stream.Close()
	if err != nil {
		s.logger.Warn("failed to gracefully close stream after authorization", zap.Error(err))
		return
	}

	node = new(Node)
	node.Country, node.Region, node.City, err = s.resolver.LookupIP(ip)
	if err != nil {
		s.logger.Error("failed to determine IP location",
			zap.Error(err),
			zap.String("os", node.OS),
			zap.String("version", node.Version),
			zap.String("app_version", node.AppVersion),
			zap.String("ip", ip),
			zap.String("user_id", node.UserID))
		return
	}

	node.ID = pid
	node.UserID = nodeId
	node.APIKey = msg.APIKey
	node.OS = uaParts[0]
	node.Version = uaParts[1]
	node.AppVersion = uaParts[2]
	node.IPv4 = ipv4
	node.IPv6 = ipv6

	err = s.router.Add(node)
	if err != nil {
		s.logger.Warn("failed to add node",
			zap.Error(err),
			zap.String("os", node.OS),
			zap.String("version", node.Version),
			zap.String("app_version", node.AppVersion),
			zap.String("ipv4", ipv4),
			zap.String("ipv6", ipv6),
			zap.String("country", node.Country),
			zap.String("region", node.Region),
			zap.String("city", node.City),
			zap.String("user_id", node.UserID))
		return
	}

	s.logger.Debug("node added",
		zap.String("os", node.OS),
		zap.String("version", node.Version),
		zap.String("app_version", node.AppVersion),
		zap.String("ip", ip),
		zap.Int("connections", len(conns)),
		zap.String("country", node.Country),
		zap.String("region", node.Region),
		zap.String("city", node.City),
		zap.String("user_id", node.UserID))

	err = s.device.Register(nodeId, net.ParseIP(ip), node.OS, node.Version, node.AppVersion)
	if err != nil {
		s.logger.Warn("failed to register device",
			zap.Error(err),
			zap.String("os", node.OS),
			zap.String("version", node.Version),
			zap.String("ip", ip),
			zap.String("country", node.Country),
			zap.String("region", node.Region),
			zap.String("city", node.City),
			zap.String("node_id", nodeId))
	}
}

func (s *ServiceAuth) OnDisconnect(pid peer.ID) {
	err := s.router.Remove(pid)
	if err != nil {
		s.logger.Error("failed to remove node", zap.Error(err))
	}
}
