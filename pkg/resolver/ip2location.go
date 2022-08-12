package resolver

import (
	"github.com/ip2location/ip2location-go/v9"
	"regexp"
	"strings"
)

type IP2Location struct {
	ipv4 *ip2location.DB
	ipv6 *ip2location.DB
}

func NewIP2Location(ipv4FilePath, ipv6FilePath string) (*IP2Location, error) {
	ipv4db, err := ip2location.OpenDB(ipv4FilePath)
	if err != nil {
		return nil, err
	}

	ipv6db, err := ip2location.OpenDB(ipv6FilePath)
	if err != nil {
		return nil, err
	}

	return &IP2Location{ipv4db, ipv6db}, nil
}

var re, _ = regexp.Compile(`[^\w]`)

func (w *IP2Location) LookupIP(addr string) (country, region, city string, err error) {
	var c ip2location.IP2Locationrecord
	if strings.Count(addr, ":") > 1 {
		c, err = w.ipv6.Get_all(addr)
	} else {
		c, err = w.ipv4.Get_all(addr)
	}
	if err != nil {
		return "", "", "", err
	}

	parts := strings.Split(c.Region, " ")

	region = strings.ToLower(parts[0])
	region = re.ReplaceAllString(region, "")

	city = re.ReplaceAllString(strings.ToLower(c.City), "")

	return strings.ToLower(c.Country_short), region, city, nil
}

func (w *IP2Location) Close() error {
	w.ipv4.Close()
	w.ipv6.Close()
	return nil
}
