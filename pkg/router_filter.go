package pkg

type Query struct {
	Country []string
	Region  []string
	Version []string
	IP      []string
	PeerID  []string
	Limit   int
	Offset  int
}

type Filter func(q *Query)

func FilterVersion(version ...string) Filter {
	return func(q *Query) {
		q.Version = version
	}
}

func FilterIP(ip ...string) Filter {
	return func(q *Query) {
		q.IP = ip
	}
}

func FilterPeerID(id ...string) Filter {
	return func(q *Query) {
		q.PeerID = id
	}
}

func FilterCountry(country ...string) Filter {
	return func(q *Query) {
		q.Country = country
	}
}

func FilterRegion(region ...string) Filter {
	return func(q *Query) {
		q.Region = region
	}
}

func FilterLimit(limit int) Filter {
	return func(q *Query) {
		q.Limit = limit
	}
}

func FilterOffset(offset int) Filter {
	return func(q *Query) {
		q.Offset = offset
	}
}
