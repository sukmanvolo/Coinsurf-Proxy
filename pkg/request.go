package pkg

import (
	"bytes"
	"github.com/libp2p/go-libp2p-core/peer"
	"time"
)

type Request struct {
	ID       []byte
	URI      []byte
	Hostname []byte

	SessionID       uint64
	SessionDuration time.Duration

	Country []byte
	Region  []byte
	City    []byte

	SOCKS5 bool

	Permissions []Permission

	UserID   string
	Password []byte
	NodeID   string
	PeerID   peer.ID

	IP   []byte
	Pool []byte

	IPv4 bool
	IPv6 bool
}

var (
	countryRandom = []byte(`rr`)
)

func (r *Request) HasPermissions(permissions ...Permission) bool {
	if r.Permissions == nil {
		return false
	}

	for _, rp := range permissions {
		var found bool
		for _, p := range r.Permissions {
			if rp == p {
				found = true
				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}

func (r *Request) IsRandomLocation() bool {
	return bytes.Compare(r.Country, countryRandom) == 0 ||
		(r.Country == nil && r.Region == nil && r.City == nil && r.IP == nil && r.Pool == nil)
}

func (r *Request) reset() {
	r.ID = nil
	r.SessionID = 0
	r.SessionDuration = 0
	r.Permissions = nil
	r.Country = nil
	r.Region = nil
	r.City = nil
	r.SOCKS5 = false
	r.IPv4 = false
	r.IPv6 = false
	r.IP = nil
	r.Pool = nil
	r.UserID = ""
	r.NodeID = ""
	r.PeerID = ""
	r.URI = nil
	r.Hostname = nil
}
