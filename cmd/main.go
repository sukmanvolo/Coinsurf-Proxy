package main

import (
	"context"
	"fmt"
	"github.com/coinsurf-com/proxy/pkg"
	"github.com/coinsurf-com/proxy/pkg/accountant"
	"github.com/coinsurf-com/proxy/pkg/auth"
	"github.com/coinsurf-com/proxy/pkg/device_registry"
	"github.com/coinsurf-com/proxy/pkg/discovery"
	"github.com/coinsurf-com/proxy/pkg/location"
	"github.com/coinsurf-com/proxy/pkg/pool"
	"github.com/coinsurf-com/proxy/pkg/resolver"
	"github.com/coinsurf-com/proxy/pkg/router"
	"github.com/go-redis/redis/v8"
	"github.com/jessevdk/go-flags"
	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-peerstore/pstoremem"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"os/signal"
	"time"
)

type config struct {
	Debug         bool   `long:"debug" env:"DEBUG"`
	IP            string `long:"ip" env:"IP" default:"Server IP"`
	DevelopmentIP string `long:"dev-ip" env:"DEV_IP" default:"" description:"The developer's IP which will be used to determine the address of the connecting client. Used only in debug mode"`
	Port          struct {
		QuicRelay   int `long:"port-quic-relay" env:"PORT_QUIC_RELAY" default:"4242" description:"QUIC port to which outgoing nodes will connect"`
		TCPRelay    int `long:"port-tcp-relay" env:"PORT_TCP_RELAY" default:"4242" description:"TCP port to which outgoing nodes will connect"`
		ProxyHTTP   int `long:"port-proxy-http" env:"PORT_PROXY_HTTP" default:"9091" description:"Port to which proxy clients will connect"`
		ProxySOCKS5 int `long:"port-proxy-socks5" env:"PORT_PROXY_SOCKS5" default:"1080" description:"Port to which proxy clients will connect"`
		HealthCheck int `long:"port-hc" env:"PORT_HC" default:"4343" description:"Port which is used to check the operation of the system"`
	}

	API struct {
		Port          int    `long:"api-port" env:"API_PORT" default:"5555" description:"Port which is used to expose API endpoint which lists all active clients"`
		Authorization string `long:"api-authorization" env:"API_AUTHORIZATION" default:"jKAHDskadjlh123jhasopasid"`
	}

	Authorization struct {
		TTL  time.Duration `long:"auth-cache-ttl" env:"AUTH_CACHE_TTL" default:"1h" description:"The lifetime of an in-memory cache entry. After the specified time it will be deleted and the next time the client accesses the proxy server it will require authorization in Redis"`
		Size int           `long:"auth-cache-size" env:"AUTH_CACHE_SIZE" default:"100000" description:"In-memory cache size for authorized users"`
	}

	Session struct {
		Duration    time.Duration `long:"session-cache-duration" env:"SESSION_DURATION" default:"10m" description:"Default sticky session lifetime"`
		DurationMax time.Duration `long:"session-cache-duration-max" env:"SESSION_DURATION_MAX" default:"60m" description:"Max lifetime of the session"`
		MaxBytes    int           `long:"session-max-bytes" env:"SESSION_MAX_BYTES" default:"100000000" description:"Session cache size"`
	}

	Redis struct {
		DSN                 string        `long:"redis" env:"REDIS" default:"redis://127.0.0.1:6379"`
		AccountantDB        string        `long:"redis-accountant-db" env:"REDIS_ACCOUNTANT_DB" default:"1" description:"The database to be used for data accounting"`
		TasksDB             string        `long:"redis-tasks-db" env:"REDIS_TASKS_DB" default:"5" description:""`
		DiscoveryDB         string        `long:"redis-db" env:"REDIS_DB" default:"3" description:"The database to be used as a registry for the proxy server"`
		DiscoveryExpiration time.Duration `long:"redis-discovery-expiration" env:"REDIS_DISCOVERY_EXPIRATION" default:"5s" description:"Time after which the registry entry will be deleted or updated"`
		Channels            struct {
			Data   string `long:"redis-ch-data" env:"REDIS_CHANNEL_DATA" default:"user.usage" description:"The channel that is used to account the traffic"`
			User   string `long:"redis-ch-user" env:"REDIS_CHANNEL_USER" default:"user.remove" description:"The channel that is used to remove the user from the in-memory authorization cache"`
			Online string `long:"redis-ch-online" env:"REDIS_CHANNEL_ONLINE" default:"user.online" description:"The channel that is used to notify when user goes online"`
		}
	}

	Accountant struct {
		AccountBytes  int64         `long:"account-bytes" env:"ACCOUNTANT_ACCOUNT_BYTES" default:"16384" description:"The minimum amount of data to record traffic during data copying. If the request was smaller than the specified value, the accounting takes place at the end of the request"`
		ChannelSize   int           `long:"account-channel-size" env:"ACCOUNTANT_CHANNEL_SIZE" default:"1000" description:"Channel size for traffic accounting. If it is too small, the requests will hang"`
		FlushInterval time.Duration `long:"accountant-flush-interval" env:"ACCOUNTANT_FLUSH_INTERVAL" default:"5s" description:"The time interval at which the data is recorded. Decreasing the interval will increase the number of records added to the channel for traffic accounting"`
	}

	GeoIP struct {
		IP2LocationV4 string `long:"geoip-ip2location-v4" env:"GEOIP_IP2LOCATION_V4" default:"files/ip2location_cities.bin" description:"Path to the file containing the base for determining IP addresses of clients"`
		IP2LocationV6 string `long:"geoip-ip2location-v6" env:"GEOIP_IP2LOCATION_V6" default:"files/ip2location_cities.bin" description:"Path to the file containing the base for determining IP addresses of clients"`
		CountriesJSON string `long:"geoip-countries" env:"GEOIP_COUNTRIES" default:"files/countries.json" description:"List of all countries available in the system. Used by the proxy server for geo-targeting"`
		RegionsJSON   string `long:"geoip-regions" env:"GEOIP_REGIONS" default:"files/regions.json" description:"List of all regions available in the system. Used by the proxy server for geo-targeting"`
		CitiesJSON    string `long:"geoip-cities" env:"GEOIP_CITIES" default:"files/cities.json" description:"List of all cities available in the system. Used by the proxy server for geo-targeting"`
	}

	Ranking struct {
		WeightDecrement int           `long:"ranking-wd" env:"RANKING_WD" default:"100" description:"The value by which the weight of the outgoing node will be increased after it is used to route the request. The greater the weight, the less likely that the given node will be used next time"`
		ResetInterval   time.Duration `long:"ranking-reset-interval" env:"RANKING_RESET_INTERVAL" default:"3m" description:"The interval in which the weight value of all outgoing nodes is reset to zero "`
	}

	Proxy struct {
		BufferSizeTCP int           `long:"proxy-buffer-size-tcp" env:"PROXY_BUFFER_SIZE_TCP" default:"4096" description:"Buffer size for copying TCP data"`
		BufferSizeUDP int           `long:"proxy-buffer-size-udp" env:"PROXY_BUFFER_SIZE_UDP" default:"65507" description:"Buffer size for copying UDP data"`
		TimeoutTCP    time.Duration `long:"proxy-timeout-tcp" env:"PROXY_TIMEOUT_TCP" default:"30s" description:"Data Transfer Deadline. If no reads were made from the node during this period, the copying process will be considered completed and the request will be reset"`
		TimeoutUDP    time.Duration `long:"proxy-timeout-udp" env:"PROXY_TIMEOUT_UDP" default:"15s" description:"Data Transfer Deadline. If no reads were made from the node during this period, the copying process will be considered completed and the request will be reset"`
		DialTimeout   time.Duration `long:"proxy-timeout-dial" env:"PROXY_TIMEOUT_DIAL" default:"5s" description:""`
	}

	Pool struct {
		Capacity           int           `long:"pool-capacity" env:"POOL_CAPACITY" default:"1000" description:"Goroutine pool capacity"`
		UserPassword       string        `long:"pool-user-password" env:"POOL_USER_PASSWORD" default:"" description:"User password which will be used to send proxy requests"`
		FetchTasksInterval time.Duration `long:"pool-fetch-tasks-interval" env:"POOL_FETCH_TASKS_INTERVAL" default:"5m" description:"Interval between fetch tasks for pools"`
		DialerTimeout      time.Duration `long:"pool-dialer-timeout" env:"POOL_DIALER_TIMEOUT" default:"10s" description:"HTTP request dialer timeout when testing proxies"`
		RequestTimeout     time.Duration `long:"pool-request-timeout" env:"POOL_REQUEST_TIMEOUT" default:"30s" description:"HTTP request timeout when testing proxies"`
		InitialDelay       time.Duration `long:"pool-dialer-delay" env:"POOL_DIALER_DELAY" default:"1m" description:"Delay before the initial pool check"`
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	var conf config
	parser := flags.NewParser(&conf, flags.Default)
	if _, err := parser.Parse(); err != nil {
		panic(err)
	}

	config := zap.NewProductionConfig()
	if conf.Debug {
		config = zap.NewDevelopmentConfig()
	}

	config.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	redisAuthOpts, err := redis.ParseURL(conf.Redis.DSN)
	if err != nil {
		logger.Panic("failed to parse Redis connection string")
	}

	redisAuth := redis.NewClient(redisAuthOpts)
	defer redisAuth.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err = redisAuth.Ping(pingCtx).Result()
	if err != nil {
		logger.Panic("failed to ping Redis", zap.Error(err))
	}

	redisAccountantOpts, err := redis.ParseURL(conf.Redis.DSN + "/" + conf.Redis.AccountantDB)
	if err != nil {
		logger.Panic("failed to parse Redis connection string")
	}

	redisAccountant := redis.NewClient(redisAccountantOpts)
	defer redisAccountant.Close()

	acc, err := accountant.NewRedis(ctx,
		conf.Accountant.AccountBytes,
		conf.Accountant.FlushInterval,
		redisAccountant,
		conf.Redis.Channels.Data,
		conf.Accountant.ChannelSize,
		logger)
	if err != nil {
		logger.Panic("failed to initialize accountant", zap.Error(err))
	}

	authentication, closer, err := auth.NewRedis(ctx,
		conf.Authorization.Size,
		conf.Authorization.TTL,
		redisAuth,
		conf.Redis.Channels.User)
	if err != nil {
		logger.Panic("failed to initialize authentication", zap.Error(err))
	}
	defer closer.Close()

	rr, err := router.NewGenji(ctx, conf.Session.MaxBytes, conf.Ranking.ResetInterval, logger)
	if err != nil {
		logger.Panic("failed to initialize node store", zap.Error(err))
	}

	ipResolver, err := resolver.NewIP2Location(conf.GeoIP.IP2LocationV4, conf.GeoIP.IP2LocationV6)
	if err != nil {
		logger.Panic("failed to initialize IP lookup",
			zap.Error(err),
			zap.String("filepath_ipv4", conf.GeoIP.IP2LocationV4),
			zap.String("filepath_ipv6", conf.GeoIP.IP2LocationV6))
	}
	defer ipResolver.Close()

	api := pkg.NewAPI(conf.API.Authorization, rr, ipResolver, logger)
	go func() {
		err = api.Listen(conf.API.Port)
		if err != nil {
			logger.Error("failed to start API server", zap.Error(err))
		}
	}()

	peers, err := pstoremem.NewPeerstore()
	if err != nil {
		logger.Panic("failed to initialize P2P Peerstore", zap.Error(err))
	}

	locationLookup, err := location.NewWorld(conf.GeoIP.CountriesJSON, conf.GeoIP.RegionsJSON, conf.GeoIP.CitiesJSON)
	if err != nil {
		logger.Panic("failed to initialize Location Lookup", zap.Error(err))
	}

	pkey, _, err := ic.GenerateKeyPair(ic.Ed25519, 0)
	if err != nil {
		logger.Panic("failed to generate priv key", zap.Error(err))
	}

	keyId, err := peer.IDFromPrivateKey(pkey)
	if err != nil {
		logger.Panic("failed to parse id", zap.Error(err))
	}

	addrs := make([]multiaddr.Multiaddr, 0)
	discoveryKeys := make([]string, 0)
	if conf.Port.QuicRelay > 0 {
		redisKey := fmt.Sprintf(":%s:/ip4/%s/udp/%d/quic/p2p/%s", conf.Redis.DiscoveryDB, conf.IP, conf.Port.QuicRelay, keyId.String())
		discoveryKeys = append(discoveryKeys, redisKey)

		host := fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic", conf.Port.QuicRelay)
		addr, err := multiaddr.NewMultiaddr(host)
		if err != nil {
			logger.Panic("failed to parse quic relay addr", zap.Error(err))
		}

		addrs = append(addrs, addr)
	}

	if conf.Port.TCPRelay > 0 {
		redisKey := fmt.Sprintf(":%s:/ip4/%s/tcp/%d/p2p/%s", conf.Redis.DiscoveryDB, conf.IP, conf.Port.TCPRelay, keyId.String())
		discoveryKeys = append(discoveryKeys, redisKey)

		host := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", conf.Port.TCPRelay)
		addr, err := multiaddr.NewMultiaddr(host)
		if err != nil {
			logger.Panic("failed to parse tcp relay addr", zap.Error(err))
		}

		addrs = append(addrs, addr)
	}

	options := []pkg.Option{
		pkg.WithPrivKey(pkey),
		pkg.WithPeerstore(peers),
		pkg.WithProxyPortHTTP(conf.Port.ProxyHTTP),
		pkg.WithProxyPortSOCKS5(conf.Port.ProxySOCKS5),
		pkg.WithIP(conf.IP),
		pkg.WithBufferSizeTCP(conf.Proxy.BufferSizeTCP),
		pkg.WithBufferSizeUDP(conf.Proxy.BufferSizeUDP),
		pkg.WithTimeoutTCP(conf.Proxy.TimeoutTCP),
		pkg.WithTimeoutUDP(conf.Proxy.TimeoutUDP),
		pkg.WithSessionDuration(conf.Session.Duration),
		pkg.WithSessionDurationMax(conf.Session.DurationMax),
		pkg.WithDialTimeout(conf.Proxy.DialTimeout),
		pkg.WithLogger(logger),
		pkg.WithAddrs(addrs...),
		pkg.WithLocationLookup(locationLookup),
		pkg.WithIPResolver(ipResolver),
	}

	if conf.DevelopmentIP != "" {
		options = append(options, pkg.WithDevelopmentIP(conf.DevelopmentIP))
	}

	deviceRegistry := device_registry.NewRedis(conf.Redis.Channels.Online, redisAuth, logger)

	redisPool, err := pool.NewRedis(conf.Redis.TasksDB,
		conf.Pool.FetchTasksInterval,
		conf.Pool.DialerTimeout,
		conf.Pool.RequestTimeout,
		conf.Pool.InitialDelay,
		conf.Pool.Capacity,
		fmt.Sprintf("%s:%d", conf.IP, conf.Port.ProxyHTTP),
		conf.Pool.UserPassword,
		redisAuth,
		rr,
		logger)
	if err != nil {
		logger.Panic("failed to initialize redis pool tester", zap.Error(err))
	}

	go func() {
		err = redisPool.Serve(ctx)
		if err != nil {
			logger.Panic("failed to serve redis pool tester", zap.Error(err))
		}
	}()

	server, err := pkg.NewServer(authentication, acc, rr, deviceRegistry, options...)
	if err != nil {
		logger.Panic("failed to initialize proxy server", zap.Error(err))
	}

	go func() {
		err = server.Listen(ctx)
		if err != nil {
			logger.Panic("failed to listen proxy server", zap.Error(err))
		}
	}()

	d, err := discovery.NewRedis(logger, redisAuth, conf.Redis.DiscoveryExpiration, discoveryKeys...)
	if err != nil {
		logger.Panic("failed to initialize redis discovery", zap.Error(err))
	}

	err = d.Join()
	if err != nil {
		logger.Panic("failed to join discovery", zap.Error(err))
	}

	defer func() {
		err = d.Leave()
		if err != nil {
			logger.Error("failed to leave discovery", zap.Error(err))
		}
	}()

	<-ctx.Done()
}
