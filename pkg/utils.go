package pkg

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"github.com/cespare/xxhash/v2"
	"github.com/coinsurf-com/proxy/internal/fasthttp"
	"hash"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

var (
	readerPool = sync.Pool{
		New: func() interface{} {
			return &bufio.Reader{}
		},
	}
	requestPool = sync.Pool{
		New: func() interface{} {
			return &Request{}
		},
	}
	hashPool       sync.Pool
	countConnsPool sync.Pool
)

func AcquireRequest() *Request {
	v := requestPool.Get()
	if v == nil {
		return &Request{}
	}

	return v.(*Request)
}

func ReleaseRequest(r *Request) {
	r.reset()
	requestPool.Put(r)
}

func acquireCountConn() *countConn {
	v := countConnsPool.Get()
	if v == nil {
		return &countConn{}
	}

	return v.(*countConn)
}

func releaseCountConn(c *countConn) {
	c.reset()
	countConnsPool.Put(c)
}

// AcquireHash returns a hash from pool
func AcquireHash() hash.Hash64 {
	v := hashPool.Get()
	if v == nil {
		return xxhash.New()
	}
	return v.(hash.Hash64)
}

// ReleaseHash returns hash to pool
func ReleaseHash(h hash.Hash64) {
	h.Reset()
	hashPool.Put(h)
}

func AcquireReader(reader io.Reader) *bufio.Reader {
	v := readerPool.Get()
	if v == nil {
		return bufio.NewReaderSize(reader, smallBufferSize)
	}
	r := v.(*bufio.Reader)
	r.Reset(reader)
	return r
}

func ReleaseReader(r *bufio.Reader) {
	readerPool.Put(r)
}

var (
	smallBufferSize = 4 * 1024
)

type hijackedConnInterface interface {
	UnsafeConn() net.Conn
}

// HijackHandler defines a hijack handler which should be
// used along with UnwrapConn.
type HijackHandler func(net.Conn)

func UnwrapConn(callback HijackHandler) fasthttp.HijackHandler {
	return func(conn net.Conn) {
		for {
			if unwrapped, ok := conn.(hijackedConnInterface); ok {
				conn = unwrapped.UnsafeConn()
			} else {
				break
			}

			callback(conn)
		}
	}
}

func parseBasicAuth(credentials []byte) (username []byte, password []byte, ok bool) {
	if len(credentials) <= 7 {
		return
	}

	var buf = make([]byte, base64.StdEncoding.DecodedLen(len(credentials)))
	w, err := base64.StdEncoding.Decode(buf, credentials[6:])
	if err != nil {
		return
	}

	s := bytes.IndexByte(buf, ':')
	if s < 0 {
		return
	}

	return buf[:s:w], buf[s+1 : w], true
}

func getRequestHeaderSize(ctx *fasthttp.RequestCtx) int64 {
	size := len(ctx.Request.Body()) + 2 // 2 for the \r\n that separates the headers and body.
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		size += len(key) + len(value) + 2 // 2 for the \r\n that separates the headers.
	})

	return int64(size)
}

const (
	HeaderTLSBrowser = "TLS-Browser"
	HeaderTLSOS      = "TLS-OS"
	HeaderIPv4       = "ipv4"
	HeaderIPv6       = "ipv6"
)

func cleanRequestHeaders(request *fasthttp.Request) {
	request.Header.Del(HeaderTLSBrowser)
	request.Header.Del(HeaderTLSOS)
	request.Header.Del(fasthttp.HeaderProxyAuthenticate)
	request.Header.Del(fasthttp.HeaderProxyAuthorization)
	request.Header.Del(fasthttp.HeaderProxyConnection)
	request.Header.Del(fasthttp.HeaderXForwardedFor)
	request.Header.Del(fasthttp.HeaderTE)
	request.Header.Del(fasthttp.HeaderTrailer)
	request.Header.Del(fasthttp.HeaderTransferEncoding)
	request.Header.Del(fasthttp.HeaderUpgrade)
	request.Header.Del(HeaderIPv4)
	request.Header.Del(HeaderIPv6)
}

type countConn struct {
	net.Conn
	copied int64
}

func (c *countConn) reset() {
	c.Conn = nil
	atomic.StoreInt64(&c.copied, 0)
}

func (c *countConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	atomic.AddInt64(&c.copied, int64(n))
	return n, err
}

func (c *countConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	atomic.AddInt64(&c.copied, int64(n))
	return n, err
}

func (c *countConn) Copied() int64 {
	return atomic.LoadInt64(&c.copied)
}
