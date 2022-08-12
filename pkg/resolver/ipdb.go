package resolver

import (
	"net"
)

type IPDB struct {
	filePath string
}

func NewIPDB(filePath string) (*IPDB, error) {
	return &IPDB{filePath: filePath}, nil
}

func (I *IPDB) LookupIP(addr net.IP) (country, region, city string, err error) {
	return "", "", "", nil
}

func (I *IPDB) Close() error {
	return nil
}
