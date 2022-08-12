package accountant

import (
	"context"
	"encoding/json"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"time"
)

type Redis struct {
	accountBytes int64
	dataChName   string
	dataCh       chan usage
	c            *redis.Client
}

func (r *Redis) AccountBytes() int64 {
	return r.accountBytes
}

type usage struct {
	userId string
	nodeId string
	data   int64
}

type result struct {
	Users map[string]int64 `json:"users"`
}

const (
	redisPublishTimeout = time.Second * 2
)

func NewRedis(signalCtx context.Context,
	accountBytes int64,
	flushPeriod time.Duration,
	rdb *redis.Client,
	dataChName string,
	chSize int,
	logger *zap.Logger) (*Redis, error) {
	dataCh := make(chan usage, chSize)

	cache := make(map[string]result)

	go func() {
		defer close(dataCh)

		ticker := time.NewTicker(flushPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-signalCtx.Done():
				return
			case <-ticker.C:
				if len(cache) == 0 {
					continue
				}

				//TODO replace with a more efficient json marshal func
				data, err := json.Marshal(cache)
				if err != nil {
					logger.Error("failed to marshal", zap.Error(err))
					cache = make(map[string]result)
					continue
				}

				cache = make(map[string]result)

				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), redisPublishTimeout)
					err := rdb.Publish(ctx, dataChName, data).Err()
					cancel()
					if err != nil {
						logger.Error("failed to publish query", zap.Error(err))
					}
				}()
			case r := <-dataCh:
				rr, ok := cache[r.nodeId]
				if !ok {
					rr = result{Users: make(map[string]int64)}
				}

				rr.Users[r.userId] += r.data

				cache[r.nodeId] = rr
			}
		}
	}()

	return &Redis{
		accountBytes: accountBytes,
		c:            rdb,
		dataCh:       dataCh,
		dataChName:   dataChName,
	}, nil
}

func (r *Redis) Decrement(_ context.Context, apiKey, nodeId string, byte int64) error {
	r.dataCh <- usage{apiKey, nodeId, byte}
	return nil
}
