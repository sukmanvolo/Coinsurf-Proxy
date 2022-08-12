package pkg

import (
	"io"
)

type Resolver interface {
	io.Closer
	LookupIP(ip string) (country, region, city string, err error)
}
