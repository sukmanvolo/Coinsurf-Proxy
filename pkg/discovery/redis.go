package discovery

import (
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"time"
)

//Redis -
type Redis struct {
	logger     *zap.Logger
	c          *redis.Client
	hosts      []string
	expiration time.Duration
	close      chan struct{}
}

func NewRedis(logger *zap.Logger, client *redis.Client, expiration time.Duration, hosts ...string) (*Redis, error) {
	if len(hosts) == 0 {
		return nil, errors.New("empty hosts list, at least one host must be specified")
	}

	return &Redis{logger, client, hosts, expiration, make(chan struct{}, 1)}, nil
}

func (c *Redis) Join() error {
	for _, host := range c.hosts {
		err := c.c.Set(context.TODO(), host, c.expiration.Seconds(), c.expiration).Err()
		if err != nil {
			return err
		}
	}

	go func() {
		t := time.NewTicker(c.expiration - time.Second)
		defer t.Stop()

		for {
			select {
			case <-c.close:
				for _, host := range c.hosts {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
					err := c.c.Del(ctx, host).Err()
					cancel()
					if err != nil {
						c.logger.Error("discovery has failed to remove record from Redis",
							zap.String("host", host), zap.Error(err))
					}
				}
				return
			case <-t.C:
				for _, host := range c.hosts {
					err := c.c.Set(context.TODO(), host, c.expiration.Seconds(), c.expiration).Err()
					if err != nil {
						c.logger.Error("discovery has failed to join Redis",
							zap.String("host", host), zap.Error(err))
					}
				}

			}
		}
	}()

	return nil
}

func (c *Redis) Leave() error {
	c.close <- struct{}{}
	close(c.close)

	return nil
}
