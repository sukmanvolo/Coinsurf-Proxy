package pkg

import (
	"bytes"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"github.com/valyala/fasthttp"
	"strconv"
	"time"
)

var (
	strHeaderProxyAuthorization = []byte(fasthttp.HeaderProxyAuthorization)
	strHeaderInvalidCredentials = "Basic realm=\"Missing credentials\"\r\n\r\n"
)

var (
	strUsernameRegion   = []byte("region")
	strUsernameCountry  = []byte("country")
	strUsernameCity     = []byte("city")
	strUsernameDuration = []byte("duration")
	strUsernameSession  = []byte("session")
	strUsernameIPv4     = []byte("ipv4")
	strUsernameIPv6     = []byte("ipv6")
	strUsernameIP       = []byte("ip")
	strUsernamePool     = []byte("pool")
	strUsernameDivider  = []byte("-")
)

type UsernameParser struct {
	sessionDuration    time.Duration
	sessionDurationMax time.Duration
	location           LocationLookup
}

func NewUsernameParser(sessionDuration, sessionDurationMax time.Duration, location LocationLookup) *UsernameParser {
	return &UsernameParser{sessionDuration: sessionDuration, sessionDurationMax: sessionDurationMax, location: location}
}

//Parse - extract targeting data from a username provided
func (s *UsernameParser) Parse(username []byte, req *Request) (err error) {
	var sessionID []byte
	params := bytes.Split(username, strUsernameDivider)
	for i, p := range params {
		i++
		if i%2 == 0 {
			continue
		}

		if bytes.EqualFold(p, strUsernameCountry) {
			if len(params) < i {
				return ErrInvalidParam
			}

			if len(params[i]) > 2 {
				return ErrInvalidParam
			}

			req.Country = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernameRegion) {
			if len(params) < i {
				return ErrInvalidParam
			}

			req.Region = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernameCity) {
			if len(params) < i {
				return ErrInvalidParam
			}

			req.City = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernameSession) {
			if len(params) < i {
				return ErrInvalidParam
			}

			sessionID = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernameDuration) {
			if len(params) < i {
				return ErrInvalidParam
			}

			duration, err := strconv.Atoi(zerocopy.String(params[i]))
			if err != nil {
				return err
			}

			req.SessionDuration = time.Duration(duration) * time.Second
			continue
		} else if bytes.EqualFold(p, strUsernameIP) {
			if len(params) < i {
				return ErrInvalidParam
			}

			req.IP = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernamePool) {
			if len(params) < i {
				return ErrInvalidParam
			}

			req.Pool = params[i]
			continue
		} else if bytes.EqualFold(p, strUsernameIPv6) {
			req.IPv6 = true
		} else if bytes.EqualFold(p, strUsernameIPv4) {
			req.IPv4 = true
		}
	}

	if req.Country == nil && req.Region == nil && req.City == nil && req.Pool == nil && req.IP == nil {
		return ErrInvalidTargeting
	}

	if req.Country != nil && req.Region == nil && req.City == nil {
		//Only country was provided
		found := s.location.FindCountry(zerocopy.String(req.Country))
		if !found {
			return ErrUnknownCountry
		}

	} else if req.Country != nil && req.City != nil {
		//Country and city was provided
		found := s.location.FindCity(zerocopy.String(req.Country), zerocopy.String(req.City))
		if !found {
			return ErrUnknownCity
		}
	} else if req.Country != nil && req.Region != nil {
		//Country and region was provided
		found := s.location.FindRegion(zerocopy.String(req.Country), zerocopy.String(req.Region))
		if !found {
			return ErrUnknownRegion
		}
	}

	if len(sessionID) > 0 {
		if req.SessionDuration <= 0 || req.SessionDuration > s.sessionDurationMax {
			req.SessionDuration = s.sessionDuration
		}

		digest := AcquireHash()
		defer ReleaseHash(digest)

		if req.Country != nil {
			_, err = digest.Write(req.Country)
			if err != nil {
				return err
			}
		}

		if req.Pool != nil {
			_, err = digest.Write(req.Pool)
			if err != nil {
				return err
			}
		}

		_, err = digest.Write(sessionID)
		if err != nil {
			return err
		}

		_, err = digest.Write(req.Password)
		if err != nil {
			return err
		}

		req.SessionID = digest.Sum64()
	}

	return
}
