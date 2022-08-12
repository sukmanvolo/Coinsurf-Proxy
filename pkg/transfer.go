package pkg

import (
	"context"
	pool "github.com/libp2p/go-buffer-pool"
	"io"
	"net"
	"time"
)

type Transfer struct {
	buffer       int
	copyDeadline time.Duration
	accountant   Accountant
}

func NewTransfer(buffer int, copyDeadline time.Duration, accountant Accountant) *Transfer {
	return &Transfer{buffer: buffer, copyDeadline: copyDeadline, accountant: accountant}
}

func (s *Transfer) Transfer(ctx context.Context, userID, nodeId string, dst, src net.Conn, close bool) (read int64, err error) {
	buf := pool.Get(s.buffer)

	defer func() {
		if close {
			_ = src.Close()
		}
		pool.Put(buf)
	}()

	var accounted, written int64
	for {
		if s.copyDeadline > 0 {
			err = src.SetReadDeadline(time.Now().Add(s.copyDeadline))
			if err != nil {
				break
			}
		}

		if accounted >= s.accountant.AccountBytes() {
			err = s.accountant.Decrement(ctx, userID, nodeId, accounted)
			accounted = 0
		}

		nr, er := src.Read(buf)
		accounted += int64(nr)
		read += int64(nr)

		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nw != nr {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}

	if accounted > 0 {
		err = s.accountant.Decrement(ctx, userID, nodeId, accounted)
	}

	return
}
