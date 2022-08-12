package pkg

import (
	"github.com/libp2p/go-libp2p-core/peer"
)

type Router interface {
	//Route - find a peer to route request to
	Route(request *Request) (*Node, error)

	//Add - targetID is a dot-separated prefix tree
	Add(*Node) error

	//Remove - targetID is a dot-separated prefix tree
	Remove(peer.ID) error

	//Count - return number of nodes based on given filters
	Count(...Filter) (int64, error)

	GetByIP(ip string) (*Node, error)

	//GetAll - list all nodes connected to the server
	GetAll(...Filter) ([]*Node, error)

	GetPoolNodes() ([]*PoolNode, error)

	//PoolAdd - added nodes to the pool
	PoolAdd(name string, id ...peer.ID) error

	//PoolDelete - delete nodes from the pool
	PoolDelete(name string, id ...peer.ID) error

	//PoolCleanNameNotIn - remove all pools where names don't match
	PoolCleanNameNotIn(names ...string) error

	//GetCountryStats - returns number of nodes per country
	GetCountryStats() (map[string]int64, error)

	//GetPoolStats - returns number of nodes per pool
	GetPoolStats() (map[string]int64, error)
}
