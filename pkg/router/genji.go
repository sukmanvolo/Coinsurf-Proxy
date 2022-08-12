package router

import (
	"context"
	"errors"
	"fmt"
	"github.com/VictoriaMetrics/fastcache"
	"github.com/coinsurf-com/proxy/internal/cache"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"github.com/coinsurf-com/proxy/pkg"
	"github.com/genjidb/genji"
	"github.com/genjidb/genji/types"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/mailru/easyjson"
	"github.com/valyala/bytebufferpool"
	"go.uber.org/zap"
	"strconv"
	"strings"
	"sync"
	"time"
)

var tables = []string{
	`
        CREATE TABLE nodes(
            peer_id              VARCHAR(64)    PRIMARY KEY,
            user_id         	 VARCHAR(36)    NOT NULL,
            ipv4	         	 VARCHAR(15)    NOT NULL,
            ipv6	         	 VARCHAR(39)    NOT NULL,
            api_key    			 VARCHAR(36)    NOT NULL,
            os	    			 VARCHAR(36)    NOT NULL,
            version    			 VARCHAR(36)    NOT NULL,
            app_version    	     VARCHAR(10)    NOT NULL,
            country         	 VARCHAR(2)     NOT NULL,
            region         		 VARCHAR(64)    NOT NULL,
            city         		 VARCHAR(64)    NOT NULL,
			ranking 		     INTEGER        DEFAULT 0,

            UNIQUE(peer_id)
        )
    `,
	`
        CREATE TABLE sessions(
            peer_id              TEXT    NOT NULL,
            session_id         	 TEXT    NOT NULL
        )
    `,
}

var indexes = []string{
	"CREATE INDEX idx_session_id ON sessions(session_id);",
	"CREATE INDEX idx_peer_id ON sessions(peer_id);",

	"CREATE INDEX idx_country ON nodes(country);",
	"CREATE INDEX idx_ipv4 ON nodes(ipv4);",
	"CREATE INDEX idx_ipv6 ON nodes(ipv6);",
	"CREATE INDEX idx_app_version ON nodes(app_version);",
	"CREATE INDEX idx_country_region ON nodes(country, region);",
	"CREATE INDEX idx_country_city ON nodes(country, city);",
}

const (
	sessionsInsert            = "INSERT INTO sessions(peer_id, session_id) VALUES(?, ?)"
	sessionsSelectByPeerID    = "SELECT session_id FROM sessions WHERE peer_id = ?"
	sessionsSelectBySessionID = "SELECT peer_id FROM sessions WHERE session_id = ?"
	sessionsDeleteByPeerID    = "DELETE FROM sessions WHERE peer_id = ?"
	sessionsDeleteBySessionID = "DELETE FROM sessions WHERE session_id = ?"
)

const (
	poolsSelectQuery           = "SELECT peer_id FROM pools WHERE name = ? ORDER BY ranking ASC LIMIT 1"
	poolsDeleteNodeQuery       = "DELETE FROM pools WHERE peer_id = ?"
	poolsSelectGroupNameQuery  = "SELECT COUNT(*) as nodes, name FROM pools GROUP BY name"
	poolsSelectAllQuery        = "SELECT name, peer_id, ranking FROM pools ORDER BY ranking DESC"
	poolsInsertAtomicQuery     = "INSERT INTO pools (peer_id, name) VALUES (?, ?) ON CONFLICT DO REPLACE"
	poolsInsertQuery           = "INSERT INTO pools (peer_id, name) VALUES"
	poolsDeleteBulkQuery       = "DELETE FROM pools WHERE name = "
	poolsPurgeQuery            = "DELETE FROM pools WHERE name "
	poolsIncrementRankingQuery = "UPDATE pools SET ranking = ranking + 1 WHERE peer_id = ?"
	poolsResetRankingQuery     = "UPDATE pools SET ranking = 0"
)

const (
	nodesInsertQuery = `INSERT INTO nodes (peer_id, country, region, city, user_id, api_key, os, version, app_version, ipv4, ipv6, ranking) 
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0) ON CONFLICT DO REPLACE`
	nodesDeleteQuery             = "DELETE FROM nodes WHERE peer_id = ?"
	nodesSelectQuery             = "SELECT peer_id, user_id, api_key, country, region, city, os, version, app_version, ipv4, ipv6 FROM nodes"
	nodesCountQuery              = "SELECT COUNT(*) as nodes FROM nodes"
	nodesSelectGroupCountryQuery = "SELECT COUNT(*) as nodes, country FROM nodes GROUP BY country"
	nodesResetRankingQuery       = "UPDATE nodes SET ranking = 0"
	nodesIncrementRankingQuery   = "UPDATE nodes SET ranking = ranking + 1 WHERE peer_id = ?"
)

type Genji struct {
	mu *sync.RWMutex

	sessions pkg.Cache

	db     *genji.DB
	pools  *genji.DB
	logger *zap.Logger
}

func NewGenji(ctx context.Context, maxBytes int, rankingReset time.Duration, logger *zap.Logger) (*Genji, error) {
	pools, err := genji.Open(":memory:")
	if err != nil {
		return nil, err
	}

	pools.WithContext(ctx)
	err = pools.Exec(
		`
        CREATE TABLE pools(
            peer_id              TEXT           PRIMARY KEY,
            name         		 TEXT           NOT NULL,
			ranking 			 INTEGER        DEFAULT 0,

            UNIQUE(peer_id)
        )
    `)
	if err != nil {
		return nil, err
	}

	for _, i := range []string{"CREATE INDEX idx_name ON pools(name);",
		"CREATE INDEX idx_pid ON pools(peer_id);"} {
		err = pools.Exec(i)
		if err != nil {
			return nil, err
		}
	}

	db, err := genji.Open(":memory:")
	if err != nil {
		return nil, err
	}

	db.WithContext(ctx)

	tx, err := db.Begin(true)
	if err != nil {
		return nil, err
	}

	for _, table := range tables {
		err = tx.Exec(table)
		if err != nil {
			return nil, err
		}
	}

	for _, index := range indexes {
		err = tx.Exec(index)
		if err != nil {
			return nil, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	go func() {
		reset := time.NewTicker(rankingReset)
		defer reset.Stop()
		defer db.Close()
		defer pools.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case <-reset.C:
				err = db.Exec(nodesResetRankingQuery)
				if err != nil {
					logger.Error("failed to reset node rankings", zap.Error(err))
				}

				err = pools.Exec(poolsResetRankingQuery)
				if err != nil {
					logger.Error("failed to reset pool rankings", zap.Error(err))
				}
			}
		}
	}()

	g := &Genji{
		mu:     new(sync.RWMutex),
		db:     db,
		pools:  pools,
		logger: logger,
	}

	options := []cache.Option{
		cache.WithOnExpired(g.onSessionExpired),
	}
	g.sessions = cache.NewFastCache(fastcache.New(maxBytes), options...)

	return g, nil
}

func (s *Genji) Add(node *pkg.Node) error {
	return s.db.Exec(nodesInsertQuery,
		peer.Encode(node.ID),
		node.Country, node.Region, node.City,
		node.UserID, node.APIKey,
		node.OS, node.Version, node.AppVersion, node.IPv4, node.IPv6)
}

func (s *Genji) Route(request *pkg.Request) (*pkg.Node, error) {
	var sessionID string
	if request.SessionID != 0 && request.IP == nil {
		sessionID = strconv.FormatUint(request.SessionID, 10)
		node, ok := s.findSession(sessionID)
		if ok {
			return node, nil
		}
	}

	node, pid, err := s.pick(request)
	if err != nil {
		return nil, err
	}

	if request.SessionID != 0 && request.IP == nil {
		data, err := easyjson.Marshal(node)
		if err != nil {
			return nil, err
		}

		err = s.sessions.SetWithExpire(sessionID, data, request.SessionDuration)
		if err != nil {
			return nil, err
		}

		err = s.db.Exec(sessionsInsert, pid, sessionID)
		if err != nil {
			return nil, err
		}
	}

	return node, nil
}

func (s *Genji) Remove(id peer.ID) error {
	pid := peer.Encode(id)

	//delete node from nodes
	err := s.db.Exec(nodesDeleteQuery, pid)
	if err != nil {
		return err
	}

	//delete node from pools
	err = s.pools.Exec(poolsDeleteNodeQuery, pid)
	if err != nil {
		return err
	}

	//delete sessions from database
	results, err := s.db.Query(sessionsSelectByPeerID, pid)
	if err != nil {
		return err
	}
	defer results.Close()

	var sessions int
	err = results.Iterate(func(d types.Document) error {
		sessionID, err := d.GetByField("session_id")
		if err != nil {
			return err
		}

		sid, ok := sessionID.V().(string)
		if !ok {
			return errors.New("invalid session_id type")
		}
		//delete sessions from cache
		s.sessions.Delete(sid)
		sessions++

		return nil
	})
	if err != nil {
		return err
	}

	if sessions == 0 {
		return nil
	}

	return s.db.Exec(sessionsDeleteByPeerID, pid)
}

func (s *Genji) PoolAdd(name string, ids ...peer.ID) error {
	if len(ids) == 0 {
		return nil
	}

	for _, id := range ids {
		pid := peer.Encode(id)
		fmt.Println("SUCCESS " + pid)

		err := s.pools.Exec(poolsInsertAtomicQuery, pid, name)
		if err != nil {
			s.logger.Error("failed to insert pool record", zap.Error(err))
		}
	}

	//buf := bytebufferpool.Get()
	//defer bytebufferpool.Put(buf)
	//buf.WriteString(poolsInsertQuery)
	//
	//var args = make([]interface{}, 0)
	//for _, id := range ids {
	//	if buf.Len() > len(poolsInsertQuery) {
	//		buf.WriteString(`, `)
	//	}
	//
	//	buf.WriteString(`(?, ?)`)
	//
	//	args = append(args, []interface{}{peer.Encode(id), name}...)
	//}
	//
	//buf.WriteString(" ON CONFLICT DO REPLACE")

	//fmt.Println(buf.String())
	//
	//return s.db.Exec(buf.String(), args...)
	return nil
}

func (s *Genji) PoolDelete(name string, ids ...peer.ID) error {
	if len(ids) == 0 {
		return nil
	}

	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)
	buf.WriteString(poolsDeleteBulkQuery)

	buf.WriteString(`"`)
	buf.WriteString(name)
	buf.WriteString(`"`)
	buf.WriteString(" AND peer_id ")

	if len(ids) == 1 {
		buf.WriteString("= ")
		buf.WriteString(`"`)
		buf.WriteString(peer.Encode(ids[0]))
		buf.WriteString(`"`)

	} else {
		buf.WriteString("IN(")
		var firstRow = true
		for _, id := range ids {
			if !firstRow {
				buf.WriteString(`,`)
			}
			pid := peer.Encode(id)

			buf.WriteString(`"`)
			buf.WriteString(pid)
			buf.WriteString(`"`)

			firstRow = false
		}
		buf.WriteString(`)`)
	}

	return s.pools.Exec(buf.String())
}

func (s *Genji) PoolCleanNameNotIn(names ...string) error {
	if len(names) == 0 {
		return nil
	}

	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)
	buf.WriteString(poolsPurgeQuery)

	if len(names) == 1 {
		buf.WriteString("!= ")
		buf.WriteString(`"`)
		buf.WriteString(names[0])
		buf.WriteString(`"`)
	} else {
		buf.WriteString("NOT IN(")
		var firstRow = true
		for _, name := range names {
			if !firstRow {
				buf.WriteString(`,`)
			}

			buf.WriteString(`"`)
			buf.WriteString(name)
			buf.WriteString(`"`)

			firstRow = false
		}
		buf.WriteString(`)`)
	}

	return s.pools.Exec(buf.String())
}

func (s *Genji) Count(filters ...pkg.Filter) (int64, error) {
	q := nodesCountQuery
	if len(filters) > 0 {
		query := new(pkg.Query)
		for _, f := range filters {
			f(query)
		}

		q = queryBuilder(q, query, true)
	}

	result, err := s.db.QueryDocument(q)
	if err != nil {
		return -1, err
	}

	nodes, err := result.GetByField("nodes")
	if err != nil {
		return -1, err
	}

	count, ok := nodes.V().(int64)
	if !ok {
		return -1, errors.New("invalid nodes count type")
	}

	return count, nil
}

func (s *Genji) GetByIP(ip string) (*pkg.Node, error) {
	q := nodesSelectQuery + " WHERE ipv4 = ? LIMIT 1"

	d, err := s.db.QueryDocument(q, ip)
	if err != nil {
		return nil, err
	}

	node, _, err := documentToNode(d)
	if err != nil {
		return nil, err
	}

	return node, nil
}

func (s *Genji) GetAll(filters ...pkg.Filter) ([]*pkg.Node, error) {
	q := nodesSelectQuery
	if len(filters) > 0 {
		query := new(pkg.Query)
		for _, f := range filters {
			f(query)
		}

		q = queryBuilder(q, query, false)
	}

	result, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes = make([]*pkg.Node, 0)
	err = result.Iterate(func(d types.Document) error {
		node, _, err := documentToNode(d)
		if err != nil {
			return err
		}

		nodes = append(nodes, node)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

func (s *Genji) GetPoolNodes() ([]*pkg.PoolNode, error) {
	tx, err := s.pools.Begin(false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result, err := tx.Query(poolsSelectAllQuery)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes = make([]*pkg.PoolNode, 0)
	err = result.Iterate(func(d types.Document) error {
		var p = new(pkg.PoolNode)

		name, err := d.GetByField("name")
		if err != nil {
			return err
		}

		name.Type().String()

		p.Name = name.String()

		peerID, err := d.GetByField("peer_id")
		if err != nil {
			return err
		}
		pid, ok := peerID.V().(string)
		if !ok {
			return errors.New("invalid peer_id type")
		}

		p.ID, err = peer.Decode(pid)
		if err != nil {
			return errors.New("failed to decode peer_id")
		}

		ranking, err := d.GetByField("ranking")
		if err != nil {
			return err
		}

		p.Ranking, ok = ranking.V().(int64)
		if !ok {
			return errors.New("invalid ranking type")
		}

		nodes = append(nodes, p)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

func (s *Genji) GetPoolStats() (map[string]int64, error) {
	result, err := s.pools.Query(poolsSelectGroupNameQuery)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes = make(map[string]int64, 0)
	err = result.Iterate(func(d types.Document) error {
		nameValue, err := d.GetByField("name")
		if err != nil {
			return err
		}

		name, ok := nameValue.V().(string)
		if !ok {
			return nil
		}

		countValue, err := d.GetByField("nodes")
		if err != nil {
			return err
		}

		count, ok := countValue.V().(int64)
		if !ok {
			return nil
		}

		nodes[name] = count

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

func (s *Genji) GetCountryStats() (map[string]int64, error) {
	result, err := s.db.Query(nodesSelectGroupCountryQuery)
	if err != nil {
		return nil, err
	}
	defer result.Close()

	var nodes = make(map[string]int64, 0)
	err = result.Iterate(func(d types.Document) error {
		countryValue, err := d.GetByField("country")
		if err != nil {
			return err
		}

		country, ok := countryValue.V().(string)
		if !ok {
			return nil
		}

		countValue, err := d.GetByField("nodes")
		if err != nil {
			return err
		}

		count, ok := countValue.V().(int64)
		if !ok {
			return nil
		}

		nodes[country] = count

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

func (s *Genji) onSessionExpired(key string, _ []byte) {
	err := s.db.Exec(sessionsDeleteBySessionID, key)
	if err != nil {
		s.logger.Error("failed to delete session", zap.Error(err))
		return
	}
}

func (s *Genji) findSession(sessionID string) (*pkg.Node, bool) {
	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)

	data, ok := s.sessions.Get(sessionID, buf.Bytes())
	if !ok {
		return nil, false
	}

	var node = new(pkg.Node)
	err := easyjson.Unmarshal(data, node)
	if err != nil {
		s.logger.Error("failed to unmarshal node", zap.Error(err))
		return nil, false
	}

	return node, true
}

func (s *Genji) pickFromPool(name string) (*pkg.Node, string, error) {
	result, err := s.pools.QueryDocument(poolsSelectQuery, name)
	if err != nil {
		return nil, "", err
	}

	peerId, err := result.GetByField("peer_id")
	if err != nil {
		return nil, "", err
	}

	pid, ok := peerId.V().(string)
	if !ok {
		return nil, "", errors.New("invalid peer_id type")
	}

	result, err = s.db.QueryDocument(nodesSelectQuery+` WHERE peer_id = ?`, pid)
	if err != nil {
		//TODO fix dirty hack
		s.pools.Exec(`DELETE FROM pools WHERE peer_id = ?`)
		fmt.Println("FFAILED2 " + pid)

		return nil, "", err
	}

	node, _, err := documentToNode(result)
	if err != nil {
		return nil, "", err
	}

	err = s.db.Exec(nodesIncrementRankingQuery, pid)
	if err != nil {
		return nil, "", err
	}

	err = s.pools.Exec(poolsIncrementRankingQuery, pid)
	if err != nil {
		return nil, "", err
	}

	return node, pid, nil
}

func (s *Genji) pick(request *pkg.Request) (*pkg.Node, string, error) {
	if request.Pool != nil {
		return s.pickFromPool(zerocopy.String(request.Pool))
	}

	query := bytebufferpool.Get()
	defer bytebufferpool.Put(query)

	_, err := query.WriteString(nodesSelectQuery)
	if err != nil {
		return nil, "", err
	}

	var args []interface{}
	if request.IsRandomLocation() {
		_, err = query.WriteString(" ORDER BY ranking ASC LIMIT 1")
	} else if request.IP != nil {
		_, err = query.WriteString(" WHERE ipv4 = ?")
		args = []interface{}{zerocopy.String(request.IP)}
	} else {
		if request.Country != nil && request.Region != nil {
			_, err = query.WriteString(" WHERE country = ? AND region = ?")
			args = []interface{}{zerocopy.String(request.Country), zerocopy.String(request.Region)}
		} else if request.Country != nil && request.City != nil {
			_, err = query.WriteString(" WHERE country = ? AND city = ?")
			args = []interface{}{zerocopy.String(request.Country), zerocopy.String(request.City)}
		} else if request.Country != nil {
			_, err = query.WriteString(" WHERE country = ?")
			args = []interface{}{zerocopy.String(request.Country)}
		} else {
			return nil, "", errors.New("invalid request targeting")
		}
		if err != nil {
			return nil, "", err
		}

		_, err = query.WriteString(" ORDER BY ranking ASC LIMIT 1")
		if err != nil {
			return nil, "", err
		}
	}

	d, err := s.db.QueryDocument(query.String(), args...)
	if err != nil {
		return nil, "", err
	}

	node, peerID, err := documentToNode(d)
	if err != nil {
		return nil, "", err
	}

	err = s.db.Exec(nodesIncrementRankingQuery, peerID)
	if err != nil {
		return nil, "", err
	}

	return node, peerID, nil
}

func documentToNode(d types.Document) (*pkg.Node, string, error) {
	peerId, err := d.GetByField("peer_id")
	if err != nil {
		return nil, "", err
	}

	apiKey, err := d.GetByField("api_key")
	if err != nil {
		return nil, "", err
	}

	userId, err := d.GetByField("user_id")
	if err != nil {
		return nil, "", err
	}

	country, err := d.GetByField("country")
	if err != nil {
		return nil, "", err
	}

	region, err := d.GetByField("region")
	if err != nil {
		return nil, "", err
	}

	city, err := d.GetByField("city")
	if err != nil {
		return nil, "", err
	}

	os, err := d.GetByField("os")
	if err != nil {
		return nil, "", err
	}

	version, err := d.GetByField("version")
	if err != nil {
		return nil, "", err
	}

	appVersion, err := d.GetByField("app_version")
	if err != nil {
		return nil, "", err
	}

	ipv4, err := d.GetByField("ipv4")
	if err != nil {
		return nil, "", err
	}

	ipv6, err := d.GetByField("ipv6")
	if err != nil {
		return nil, "", err
	}

	var ok bool
	var node = new(pkg.Node)
	node.APIKey, ok = apiKey.V().(string)
	if !ok {
		return nil, "", errors.New("invalid api_key data type")
	}

	node.UserID, ok = userId.V().(string)
	if !ok {
		return nil, "", errors.New("invalid user_id data type")
	}

	node.Country, ok = country.V().(string)
	if !ok {
		return nil, "", errors.New("invalid country data type")
	}

	node.Region, ok = region.V().(string)
	if !ok {
		return nil, "", errors.New("invalid region data type")
	}

	node.City, ok = city.V().(string)
	if !ok {
		return nil, "", errors.New("invalid city data type")
	}

	node.OS, ok = os.V().(string)
	if !ok {
		return nil, "", errors.New("invalid os data type")
	}

	node.Version, ok = version.V().(string)
	if !ok {
		return nil, "", errors.New("invalid version data type")
	}

	node.AppVersion, ok = appVersion.V().(string)
	if !ok {
		return nil, "", errors.New("invalid app_version data type")
	}

	node.IPv4, ok = ipv4.V().(string)
	if !ok {
		return nil, "", errors.New("invalid ipv4 data type")
	}

	node.IPv6, ok = ipv6.V().(string)
	if !ok {
		return nil, "", errors.New("invalid ipv6 data type")
	}

	var peerID string
	peerID, ok = peerId.V().(string)
	if !ok {
		return nil, "", errors.New("invalid peer_id data type")
	}

	node.ID, err = peer.Decode(peerID)
	if err != nil {
		return nil, "", err
	}

	return node, peerID, nil
}

func queryBuilder(query string, q *pkg.Query, countQuery bool) string {
	if len(q.Country) > 0 {
		if !strings.Contains(query, "WHERE") {
			query = query + " WHERE "
		}

		if len(q.Country) == 1 {
			query = query + `country = "` + strings.ToLower(q.Country[0]) + `"`
		} else {
			countries := make([]string, len(q.Country))
			for i, country := range q.Country {
				countries[i] = `"` + strings.ToLower(country) + `"`
			}

			query = query + "country IN(" + strings.Join(countries, ",") + ")"
		}
	}

	if len(q.PeerID) > 0 {
		if !strings.Contains(query, "WHERE") {
			query = query + " WHERE "
		}

		if len(q.PeerID) == 1 {
			query = query + `peer_id = "` + strings.ToLower(q.PeerID[0]) + `"`
		} else {
			nodes := make([]string, len(q.PeerID))
			for i, id := range q.PeerID {
				nodes[i] = `"` + strings.ToLower(id) + `"`
			}

			query = query + "peer_id IN(" + strings.Join(nodes, ",") + ")"
		}
	}

	if len(q.IP) > 0 {
		if !strings.Contains(query, "WHERE") {
			query = query + " WHERE "
		}

		if len(q.IP) == 1 {
			query = query + `ipv4 = "` + strings.ToLower(q.IP[0]) + `"`
		} else {
			ips := make([]string, len(q.IP))
			for i, ip := range q.IP {
				ips[i] = `"` + strings.ToLower(ip) + `"`
			}

			query = query + "ipv4 IN(" + strings.Join(ips, ",") + ")"
		}
	}

	if len(q.Region) > 0 {
		if !strings.Contains(query, "WHERE") {
			query = query + " WHERE "
		} else {
			query = query + " AND "
		}

		if len(q.Region) == 1 {
			query = query + `region = "` + q.Region[0] + `"`
		} else {
			regions := make([]string, len(q.Region))
			for i, region := range q.Region {
				regions[i] = `"` + region + `"`
			}

			query = query + "region IN(" + strings.Join(regions, ",") + ")"
		}
	}

	if len(q.Version) > 0 {
		if !strings.Contains(query, "WHERE") {
			query = query + " WHERE "
		} else {
			query = query + " AND "
		}

		if len(q.Version) == 1 {
			query = query + `app_version = "` + q.Version[0] + `"`
		} else {
			versions := make([]string, len(q.Version))
			for i, version := range q.Version {
				versions[i] = `"` + version + `"`
			}

			query = query + "app_version IN(" + strings.Join(versions, ",") + ")"
		}
	}

	if !countQuery && q.Limit > 0 {
		query = query + " LIMIT " + strconv.Itoa(q.Limit)
	}

	if !countQuery && q.Offset > 0 {
		query = query + " OFFSET " + strconv.Itoa(q.Offset)
	}

	return query
}
