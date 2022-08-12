package pkg

import (
	"github.com/libp2p/go-libp2p-core/peer"
)

type PoolNode struct {
	ID      peer.ID
	Name    string
	Ranking int64
}

//easyjson:json
type Node struct {
	ID peer.ID `json:"id"`

	shortId []byte
	fullId  []byte

	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`

	// Identification
	UserID     string `json:"user_id"`
	APIKey     string `json:"api_key"`
	OS         string `json:"os"`
	Version    string `json:"version"`
	AppVersion string `json:"app_version"`

	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
}

func (n *Node) IP() string {
	if n.IPv4 != "" {
		return n.IPv4
	}

	return n.IPv6
}

func (n *Node) FullID() []byte {
	if len(n.fullId) > 0 {
		return n.fullId
	}

	id := n.ID.String()

	n.fullId = make([]byte, 0, 2+len(n.Region)+len(n.City)+len(id)+5)
	n.fullId = append(n.fullId, n.Country+"."...)
	n.fullId = append(n.fullId, n.Region+"."...)
	n.fullId = append(n.fullId, n.City+"."...)
	n.fullId = append(n.fullId, n.ID.String()+"."...)

	return n.fullId
}

func (n *Node) ShortID() []byte {
	if len(n.shortId) > 0 {
		return n.shortId
	}

	id := n.ID.String()

	n.shortId = make([]byte, 0, 2+len(n.City)+len(id)+5)
	n.shortId = append(n.shortId, n.Country+"."...)
	n.shortId = append(n.shortId, n.City+"."...)
	n.shortId = append(n.shortId, id+"."...)
	//n.shortId = append(n.shortId, n.UserID...)

	return n.shortId
}
