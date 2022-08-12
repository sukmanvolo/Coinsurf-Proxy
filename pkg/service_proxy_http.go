package pkg

import (
	"bytes"
	"context"
	"github.com/coinsurf-com/proxy/internal/fasthttp"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"github.com/libp2p/go-libp2p-core/network"
	"go.uber.org/zap"
	"net"
	"net/http"
	"sync"
	"time"
)

type ServiceProxyHTTP struct {
	name       string
	proxyAddr  string
	username   *UsernameParser
	auth       Auth
	accountant Accountant
	router     Router
	dialer     *Dialer
	transfer   *Transfer
	logger     *zap.Logger
}

func NewServiceProxyHTTP(name string, proxyAddr string, transfer *Transfer, username *UsernameParser, auth Auth, accountant Accountant, router Router, dialer *Dialer, logger *zap.Logger) *ServiceProxyHTTP {
	return &ServiceProxyHTTP{
		name:       name,
		proxyAddr:  proxyAddr,
		username:   username,
		auth:       auth,
		accountant: accountant,
		router:     router,
		dialer:     dialer,
		transfer:   transfer,
		logger:     logger,
	}
}

func (s *ServiceProxyHTTP) Serve(ctx context.Context) error {
	s.logger.Info("Starting HTTP proxy service " + s.proxyAddr)
	defer s.logger.Info("Stopping HTTP proxy service " + s.proxyAddr)

	//Start HTTP proxy
	proxyServer := &fasthttp.Server{
		Handler:           s.httpProxyHandler,
		IdleTimeout:       time.Second * 60,
		StreamRequestBody: true,
		CloseOnShutdown:   true,
		DisableKeepalive:  false,
		TCPKeepalive:      true,
		KeepHijackedConns: false,

		WriteTimeout: time.Second * 30,
		ReadTimeout:  time.Second * 30,

		DisablePreParseMultipartForm:  true,
		DisableHeaderNamesNormalizing: true,

		NoDefaultServerHeader: true,
		NoDefaultContentType:  true,
		NoDefaultDate:         true,
	}

	go proxyServer.ListenAndServe(s.proxyAddr)
	<-ctx.Done()

	return proxyServer.Shutdown()
}

//httpProxyHandler - global proxy handler, performs initial request parsing and authorization
func (s *ServiceProxyHTTP) httpProxyHandler(ctx *fasthttp.RequestCtx) {
	request := AcquireRequest()

	err := s.parseRequest(ctx, request)
	if err == ErrInvalidAuth {
		s.httpError(ctx, fasthttp.StatusProxyAuthRequired)
		ReleaseRequest(request)
		return
	} else if err == ErrInvalidTargeting {
		//in case of too many locations were specified
		s.httpError(ctx, fasthttp.StatusRequestURITooLong)
		ReleaseRequest(request)
		return
	} else if err == ErrUnknownCountry {
		s.httpError(ctx, fasthttp.StatusPreconditionFailed)
		ReleaseRequest(request)
		return
	} else if err == ErrUnknownRegion {
		s.httpError(ctx, fasthttp.StatusPreconditionFailed)
		ReleaseRequest(request)
		return
	} else if err == ErrUnknownCity {
		s.httpError(ctx, fasthttp.StatusPreconditionFailed)
		ReleaseRequest(request)
		return
	} else if err != nil {
		s.httpError(ctx, fasthttp.StatusBadRequest)
		ReleaseRequest(request)
		return
	}

	cleanRequestHeaders(&ctx.Request)

	request.UserID, _, err = s.auth.Authenticate(ctx, request.Password)
	if err == ErrUnauthorized {
		s.logger.Warn("failed to authorize",
			zap.String("password", zerocopy.String(request.Password)),
			zap.Error(err))
		s.httpError(ctx, fasthttp.StatusProxyAuthRequired)
		ReleaseRequest(request)
		return
	} else if err == ErrBlockedDomain || err == ErrBlockedIP {
		s.httpError(ctx, fasthttp.StatusForbidden)
		ReleaseRequest(request)
		return
	} else if err == ErrDataRanOut {
		s.httpError(ctx, fasthttp.StatusUnauthorized)
		ReleaseRequest(request)
		return
	} else if err != nil {
		s.httpError(ctx, fasthttp.StatusInternalServerError)
		ReleaseRequest(request)
		return
	}

	node, err := s.router.Route(request)
	if err != nil {
		s.logger.Error("failed to select peer",
			zap.Error(err),
			zap.String("pool", zerocopy.String(request.Pool)),
			zap.String("ip", zerocopy.String(request.IP)),
			zap.Bool("session", request.SessionID > 0),
			zap.String("country", zerocopy.String(request.Country)),
			zap.String("region", zerocopy.String(request.Region)),
			zap.String("city", zerocopy.String(request.City)),
		)
		s.httpError(ctx, fasthttp.StatusExpectationFailed)
		ReleaseRequest(request)
		return
	}

	request.PeerID = node.ID
	request.NodeID = node.UserID

	if request.IPv4 {
		ctx.Request.Header.Set(HeaderIPv4, "1")
	}

	if request.IPv6 {
		ctx.Request.Header.Set(HeaderIPv6, "1")
	}

	stream, err := s.dialer.Dial(request)
	if err != nil {
		s.dialer.Disconnect(request.PeerID)
		s.logger.Warn("failed to open stream", zap.Error(err))
		s.httpError(ctx, fasthttp.StatusGatewayTimeout)
		ReleaseRequest(request)
		return
	}

	if err = stream.Scope().SetService(s.name); err != nil {
		s.logger.Warn("failed to set service",
			zap.String("service", s.name),
			zap.Error(err))
		stream.Close()
		s.httpError(ctx, fasthttp.StatusGatewayTimeout)
		ReleaseRequest(request)
		return
	}

	if ctx.IsConnect() {
		s.serveHTTPS(ctx, request, node, stream)
		return
	}

	s.serveHTTP(ctx, request, node, stream)
}

//serveHTTPS - handles https requests to proxy
func (s *ServiceProxyHTTP) serveHTTPS(ctx *fasthttp.RequestCtx, request *Request, node *Node, stream network.Stream) {
	headerSize := getRequestHeaderSize(ctx)
	err := s.accountant.Decrement(context.TODO(), request.UserID, request.NodeID, headerSize)
	if err != nil {
		s.httpError(ctx, http.StatusInternalServerError)
		stream.Close()
		ReleaseRequest(request)
		s.logger.Warn("failed to decrement headers",
			zap.String("ip", node.IP()),
			zap.Error(err))
		return
	}

	//write connect header to stream to initiate connection
	_, err = ctx.Request.Header.WriteTo(stream)
	if err != nil {
		s.dialer.Disconnect(request.PeerID)
		s.logger.Warn("failed to write headers",
			zap.String("ip", node.IP()),
			zap.Error(err))
		s.httpError(ctx, fasthttp.StatusGone)
		//stream.Close()
		ReleaseRequest(request)
		return
	}

	ctx.Response.ImmediateHeaderFlush = true
	ctx.Response.SkipBody = true

	//read connection response from peer, status code must be 200 OK
	reader := AcquireReader(stream)
	err = ctx.Response.Read(reader)
	if err != nil {
		s.dialer.Disconnect(request.PeerID)
		s.httpError(ctx, fasthttp.StatusGone)
		ReleaseRequest(request)
		ReleaseReader(reader)
		//stream.Close()
		s.logger.Warn("failed to read headers",
			zap.String("ip", node.IP()),
			zap.Error(err))
		return
	}
	ReleaseReader(reader)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		s.httpError(ctx, fasthttp.StatusGone)
		ReleaseRequest(request)
		//stream.Close()
		s.logger.Warn("bad response status",
			zap.String("ip", node.IP()),
			zap.Error(err))
		return
	}

	ctx.Response.Header.Del(fasthttp.HeaderTransferEncoding)
	ctx.Response.Header.Del(fasthttp.HeaderConnection)
	ctx.Response.Header.Del(fasthttp.HeaderContentLength)

	ctx.Hijack(UnwrapConn(func(conn net.Conn) {
		ws := &netStream{stream}

		var wg = new(sync.WaitGroup)
		wg.Add(2)
		go func() {
			s.transfer.Transfer(ctx, request.UserID, request.NodeID, ws, conn, false)
			wg.Done()
		}()

		go func() {
			s.transfer.Transfer(ctx, request.UserID, request.NodeID, conn, ws, true)
			wg.Done()
		}()
		wg.Wait()

		ReleaseRequest(request)
	}))
}

//serverHTTP - handles plain http requests to proxy
func (s *ServiceProxyHTTP) serveHTTP(ctx *fasthttp.RequestCtx, request *Request, node *Node, stream network.Stream) {
	cc := acquireCountConn()
	reader := AcquireReader(cc)
	defer func() {
		ReleaseRequest(request)
		ReleaseReader(reader)
		releaseCountConn(cc)
		stream.Close()
	}()

	cc.Conn = &netStream{stream}

	ctx.Request.Header.ResetConnectionClose()
	_, err := ctx.Request.WriteTo(cc)
	if err != nil {
		s.logger.Warn("failed to write request",
			zap.String("ip", node.IP()),
			zap.Error(err))
		s.httpError(ctx, fasthttp.StatusGone)
		return
	}

	err = ctx.Response.Read(reader)
	if err != nil {
		s.dialer.Disconnect(request.PeerID)
		s.logger.Warn("failed to read response",
			zap.String("ip", node.IP()),
			zap.Error(err))
		s.httpError(ctx, fasthttp.StatusGatewayTimeout)
		return
	}

	err = s.accountant.Decrement(ctx, request.UserID, request.NodeID, cc.Copied())
	if err != nil {
		s.logger.Error("accountant decrement",
			zap.String("ip", node.IP()),
			zap.Error(err))
	}
}

//httpError -responds with http error
func (s *ServiceProxyHTTP) httpError(ctx *fasthttp.RequestCtx, status int) {
	ctx.Response.Reset()

	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.Response.Header.Set(fasthttp.HeaderDate, time.Now().Format(time.RFC1123))

	//all headers below this code will be ignored
	switch status {
	case fasthttp.StatusProxyAuthRequired:
		ctx.Response.Header.Set(fasthttp.HeaderProxyAuthenticate, strHeaderInvalidCredentials)
	}

	ctx.SetStatusCode(status)
	ctx.Response.SetConnectionClose()
}

//parseRequest - fills Request struct with data
func (s *ServiceProxyHTTP) parseRequest(ctx *fasthttp.RequestCtx, req *Request) error {
	authStr := ctx.Request.Header.Peek(fasthttp.HeaderProxyAuthorization)
	if authStr == nil {
		ctx.Request.Header.VisitAll(func(key, value []byte) {
			if bytes.EqualFold(key, strHeaderProxyAuthorization) {
				authStr = value
			}
		})
	}

	if authStr == nil {
		return ErrInvalidAuth
	}

	var username []byte
	var ok bool
	username, req.Password, ok = parseBasicAuth(authStr)
	if !ok {
		return ErrInvalidAuth
	}

	err := s.username.Parse(username, req)
	if err != nil {
		return err
	}

	return nil
}

type netStream struct {
	network.Stream
}

func (s *netStream) LocalAddr() net.Addr {
	return nil
}

func (s *netStream) RemoteAddr() net.Addr {
	return nil
}
