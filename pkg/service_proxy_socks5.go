package pkg

import (
	"context"
	"errors"
	"fmt"
	"github.com/patrickmn/go-cache"
	"github.com/txthinking/socks5"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ServiceProxySOCKS5 struct {
	name         string
	proxyAddr    string
	username     *UsernameParser
	auth         Auth
	serverAddr   *net.UDPAddr
	udpAddr      *net.UDPAddr
	udpConn      *net.UDPConn
	accountant   Accountant
	router       Router
	udpTimeout   time.Duration
	udpBuffer    int
	transferTCP  *Transfer
	dialer       *Dialer
	Handle       *DefaultHandle
	requests     *cache.Cache
	exchangesUDP *cache.Cache
	srcUDP       *cache.Cache
	logger       *zap.Logger
}

func NewServiceProxySOCKS5(name string, proxyAddr, ip string, bufferSizeUDP int, udpTimeout time.Duration, transferTCP *Transfer, username *UsernameParser, auth Auth, accountant Accountant, router Router, dialer *Dialer, logger *zap.Logger) (*ServiceProxySOCKS5, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", proxyAddr)
	if err != nil {
		return nil, err
	}

	serverAddr, err := net.ResolveUDPAddr("udp", ip+proxyAddr)
	if err != nil {
		return nil, err
	}

	exchangesUDP := cache.New(cache.NoExpiration, cache.NoExpiration)
	srcUDP := cache.New(cache.NoExpiration, cache.NoExpiration)
	requests := cache.New(cache.NoExpiration, cache.NoExpiration)

	return &ServiceProxySOCKS5{
		name:         name,
		proxyAddr:    proxyAddr,
		username:     username,
		serverAddr:   serverAddr,
		udpAddr:      udpAddr,
		auth:         auth,
		accountant:   accountant,
		router:       router,
		dialer:       dialer,
		transferTCP:  transferTCP,
		udpBuffer:    bufferSizeUDP,
		Handle:       &DefaultHandle{},
		udpTimeout:   udpTimeout,
		exchangesUDP: exchangesUDP,
		srcUDP:       srcUDP,
		requests:     requests,
		logger:       logger,
	}, nil
}

func (s *ServiceProxySOCKS5) Serve(ctx context.Context) error {
	s.logger.Info("Starting SOCKS5 proxy service " + s.proxyAddr)
	defer s.logger.Info("Stopping SOCKS5 proxy service " + s.proxyAddr)

	listener, err := net.Listen("tcp", s.proxyAddr)
	if err != nil {
		return err
	}

	defer listener.Close()

	//Listen TCP
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				s.logger.Error("failed to accept socks5 connection", zap.Error(err))
				continue
			}

			go func() {
				request := AcquireRequest()

				err := s.negotiate(request, conn)
				if err != nil {
					ReleaseRequest(request)
					s.logger.Error("failed to negotiate socks5", zap.Error(err))
					return
				}

				r, err := socks5.NewRequestFrom(conn)
				if err != nil {
					ReleaseRequest(request)
					s.logger.Error("failed to create request", zap.Error(err))
					return
				}

				switch r.Cmd {
				case socks5.CmdConnect:
					err = s.handleTCP(request, r, conn)
					if err != nil {
						ReleaseRequest(request)
						s.logger.Error("failed to handle socks5 TCP", zap.Error(err))
					}
					break
				case socks5.CmdUDP:
					if request.HasPermissions(PermissionUDP) {
						err = s.handleUDP(request, r, conn)
						if err != nil {
							ReleaseRequest(request)
							s.logger.Error("failed to handle socks5 UDP", zap.Error(err))
						}
						return
					}

					ReleaseRequest(request)
					_ = replySocks(r, conn, socks5.RepCommandNotSupported)
					break
				default:
					ReleaseRequest(request)
					_ = replySocks(r, conn, socks5.RepCommandNotSupported)
					break
				}
			}()
		}
	}()

	//Listen UDP
	go func() {
		var err error
		s.udpConn, err = net.ListenUDP("udp", s.udpAddr)
		if err != nil {
			s.logger.Error("failed to listen udp", zap.Error(err))
			return
		}

		defer s.udpConn.Close()
		for {
			b := make([]byte, s.udpBuffer)
			n, addr, err := s.udpConn.ReadFromUDP(b)
			if err != nil {
				s.logger.Error("failed tp read from udp", zap.Error(err))
				continue
			}

			go func(addr *net.UDPAddr, b []byte) {
				d, err := socks5.NewDatagramFromBytes(b)
				if err != nil {
					s.logger.Warn("failed to read datagram from udp", zap.Error(err))
					return
				}
				if d.Frag != 0x00 {
					return
				}

				if err := s.Handle.UDPHandle(s, addr, d); err != nil {
					s.logger.Error("failed to handle udp", zap.Error(err))
					return
				}
			}(addr, b[0:n])
		}
	}()

	<-ctx.Done()

	return ctx.Err()
}

func (s *ServiceProxySOCKS5) handleUDP(request *Request, r *socks5.Request, conn net.Conn) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return errors.New("conn is not a *net.TCPConn")
	}

	caddr, err := r.UDP(tcpConn, s.serverAddr)
	if err != nil {
		return err
	}

	h, _, err := net.SplitHostPort(tcpConn.RemoteAddr().String())
	if err != nil {
		return err
	}

	key := net.JoinHostPort(h, strconv.Itoa(caddr.Port))

	s.requests.Set(key, request, -1)

	io.Copy(ioutil.Discard, tcpConn)

	return nil
}

func (s *ServiceProxySOCKS5) handleTCP(request *Request, r *socks5.Request, conn net.Conn) error {
	stream, err := s.dialer.Dial(request)
	if err != nil {
		return err
	}

	err = stream.Scope().SetService(s.name)
	if err != nil {
		stream.Close()
		_ = replySocks(r, conn, socks5.RepServerFailure)
		return err
	}

	_, err = r.WriteTo(stream)
	if err != nil {
		stream.Close()
		_ = replySocks(r, conn, socks5.RepConnectionRefused)
		return err
	}

	ws := &netStream{stream}

	ctx := context.Background()
	var wg = new(sync.WaitGroup)
	wg.Add(2)
	go func() {
		_, _ = s.transferTCP.Transfer(ctx, request.UserID, request.NodeID, ws, conn, false)
		wg.Done()
	}()

	go func() {
		_, _ = s.transferTCP.Transfer(ctx, request.UserID, request.NodeID, conn, ws, true)
		wg.Done()
	}()
	wg.Wait()

	return nil
}

func (s *ServiceProxySOCKS5) negotiate(request *Request, conn net.Conn) error {
	r, err := socks5.NewNegotiationRequestFrom(conn)
	if err != nil {
		return err
	}

	var found bool
	for _, m := range r.Methods {
		if m == socks5.MethodUsernamePassword {
			found = true
			break
		}
	}

	if !found {
		rp := socks5.NewNegotiationReply(socks5.MethodUnsupportAll)
		if _, err := rp.WriteTo(conn); err != nil {
			return err
		}
	}

	rp := socks5.NewNegotiationReply(socks5.MethodUsernamePassword)
	if _, err := rp.WriteTo(conn); err != nil {
		return err
	}

	urq, err := socks5.NewUserPassNegotiationRequestFrom(conn)
	if err != nil {
		return err
	}

	request.UserID, request.Permissions, err = s.auth.Authenticate(context.Background(), urq.Passwd)
	if err != nil {
		urp := socks5.NewUserPassNegotiationReply(socks5.UserPassStatusFailure)
		_, _ = urp.WriteTo(conn)

		return err
	}

	err = s.username.Parse(urq.Uname, request)
	if err != nil {
		urp := socks5.NewUserPassNegotiationReply(socks5.UserPassStatusFailure)
		_, _ = urp.WriteTo(conn)

		return err
	}

	urp := socks5.NewUserPassNegotiationReply(socks5.UserPassStatusSuccess)
	if _, err := urp.WriteTo(conn); err != nil {
		return err
	}

	node, err := s.router.Route(request)
	if err != nil {
		return err
	}

	request.PeerID = node.ID
	request.NodeID = node.UserID

	return err
}

type DefaultHandle struct {
}

// UDPHandle auto handle packet. You may prefer to do yourself.
func (h *DefaultHandle) UDPHandle(s *ServiceProxySOCKS5, addr *net.UDPAddr, d *socks5.Datagram) error {
	src := addr.String()
	var ch chan byte
	send := func(ue *socks5.UDPExchange, data []byte) error {
		select {
		case <-ch:
			return fmt.Errorf("this udp address %s is not associated with tcp", src)
		default:
			_, err := ue.RemoteConn.Write(data)
			if err != nil {
				return err
			}
		}
		return nil
	}

	key := addr.String()
	r, ok := s.requests.Get(key)
	if !ok {
		return errors.New("missing request")
	}

	request, ok := r.(*Request)
	if !ok {
		s.requests.Delete(key)
		return errors.New("invalid request type")
	}

	stream, err := s.dialer.Dial(request)
	if err != nil {
		return err
	}

	err = stream.Scope().SetService(s.name)
	if err != nil {
		stream.Close()
		return err
	}

	var onReturn = func() {
		s.requests.Delete(key)
		ReleaseRequest(request)
		stream.Close()
	}

	dst := d.Address()
	var ue *socks5.UDPExchange
	iue, ok := s.exchangesUDP.Get(src + dst)
	if ok {
		fmt.Println("exchangesUDP found. Should request be released here?")
		ue = iue.(*socks5.UDPExchange)
		return send(ue, d.Data)
	}

	var laddr *net.UDPAddr
	any, ok := s.srcUDP.Get(src + dst)
	if ok {
		laddr = any.(*net.UDPAddr)
	}
	raddr, err := net.ResolveUDPAddr("udp", dst)
	if err != nil {
		onReturn()
		return err
	}

	rc, err := socks5.Dial.DialUDP("udp", laddr, raddr)
	if err != nil {
		if !strings.Contains(err.Error(), "address already in use") {
			onReturn()
			return err
		}
		rc, err = socks5.Dial.DialUDP("udp", nil, raddr)
		if err != nil {
			onReturn()
			return err
		}
		laddr = nil
	}
	if laddr == nil {
		s.srcUDP.Set(src+dst, rc.LocalAddr().(*net.UDPAddr), -1)
	}

	ue = &socks5.UDPExchange{
		ClientAddr: addr,
		RemoteConn: rc,
	}

	if err := send(ue, d.Data); err != nil {
		ue.RemoteConn.Close()
		onReturn()
		return err
	}
	s.exchangesUDP.Set(src+dst, ue, -1)
	go func(ue *socks5.UDPExchange, dst string) {
		defer func() {
			ue.RemoteConn.Close()
			s.exchangesUDP.Delete(ue.ClientAddr.String() + dst)
			onReturn()
		}()

		var b [65507]byte
		for {
			select {
			case <-ch:
				return
			default:
				if s.udpTimeout != 0 {
					if err := ue.RemoteConn.SetDeadline(time.Now().Add(s.udpTimeout)); err != nil {
						log.Println(err)
						return
					}
				}
				n, err := ue.RemoteConn.Read(b[:])
				if err != nil {
					return
				}

				a, addr, port, err := socks5.ParseAddress(dst)
				if err != nil {
					log.Println(err)
					return
				}
				d1 := socks5.NewDatagram(a, addr, port, b[0:n])
				if _, err := s.udpConn.WriteToUDP(d1.Bytes(), ue.ClientAddr); err != nil {
					return
				}
			}
		}
	}(ue, dst)

	return nil
}

func replySocks(req *socks5.Request, rw io.ReadWriter, rep byte) error {
	var reply *socks5.Reply
	if req.Atyp == socks5.ATYPIPv4 || req.Atyp == socks5.ATYPDomain {
		reply = socks5.NewReply(rep, socks5.ATYPIPv4, []byte{0x00, 0x00, 0x00, 0x00}, []byte{0x00, 0x00})
	} else {
		reply = socks5.NewReply(rep, socks5.ATYPIPv6, []byte(net.IPv6zero), []byte{0x00, 0x00})
	}
	_, err := reply.WriteTo(rw)
	return err
}
