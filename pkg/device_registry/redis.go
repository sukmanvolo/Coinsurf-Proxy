package device_registry

import (
	"context"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"net"
	"strings"
	"time"
)

const publishTimeout = time.Second * 3

type Redis struct {
	channel string
	logger  *zap.Logger
	c       *redis.Client
}

func NewRedis(channel string, c *redis.Client, logger *zap.Logger) *Redis {
	return &Redis{channel: channel, logger: logger, c: c}
}

func (r *Redis) Register(nodeId string, ip net.IP, os, version string, appVersion string) error {
	value := strings.Join([]string{nodeId, ip.String(), os, version, appVersion}, ",")

	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()

	_, err := r.c.Publish(ctx, r.channel, value).Result()
	return err
}
