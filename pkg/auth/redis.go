package auth

import (
	"context"
	"github.com/bluele/gcache"
	"github.com/coinsurf-com/proxy/internal/keys"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"github.com/coinsurf-com/proxy/pkg"
	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"io"
	"time"
)

//Redis - based authentication backed with gcache (https://github.com/bluele/gcache). All user
//records are getting cached for a specified period of time. During initialization you must provide number of records
//to keep in memory and Redis channel which is used to notify that user must be removed from cache (in case when he ran out of data)
type Redis struct {
	c          *redis.Client
	cache      gcache.Cache
	expiration time.Duration
}

func NewRedis(signalCtx context.Context, records int, expiration time.Duration, rdb *redis.Client, removeUserCh string) (*Redis, io.Closer, error) {
	cache := gcache.New(records).
		LRU().
		Build()

	r := &Redis{
		c:          rdb,
		cache:      cache,
		expiration: expiration,
	}

	go func() {
		deleteCh := rdb.Subscribe(signalCtx, removeUserCh).Channel()
		defer func() {
			cache.Purge()
		}()

		for {
			select {
			case <-signalCtx.Done():
				return
			case data := <-deleteCh:
				cache.Remove(data.Payload)
				cache.Remove(keys.PermissionKey(zerocopy.Bytes(data.Payload)))
			}
		}
	}()

	return r, rdb, nil
}

func (r *Redis) Authenticate(ctx context.Context, apiKey []byte) (string, []pkg.Permission, error) {
	var key string
	var err error
	var permissions = make([]pkg.Permission, 0)
	var userID string
	var ok bool

	var cached = true
	u, err := r.cache.Get(zerocopy.String(apiKey))
	if err == gcache.KeyNotFoundError {
		cached = false

		key = keys.UserKey(apiKey)
		userMetadata, err := r.c.Get(ctx, key).Bytes()
		if err == redis.Nil {
			return "", nil, pkg.ErrUnauthorized
		} else if err != nil {
			return "", nil, err
		}

		userID = gjson.GetBytes(userMetadata, "id").String()
		if gjson.GetBytes(userMetadata, "udp").Exists() {
			permissions = append(permissions, pkg.PermissionUDP)
		}

		expiration, err := r.c.TTL(ctx, key).Result()
		if err != redis.Nil && err != nil {
			return "", nil, err
		}

		if expiration <= 0 || expiration > r.expiration {
			expiration = r.expiration
		}

		if len(permissions) > 0 {
			err = r.cache.SetWithExpire(keys.PermissionKey(apiKey), permissions, expiration)
			if err != nil {
				return "", nil, errors.Wrap(err, "failed to set permissions")
			}
		}

		err = r.cache.SetWithExpire(zerocopy.String(apiKey), userID, expiration)
		return userID, permissions, err
	} else if err != nil {
		return "", nil, err
	}

	if cached {
		perms, err := r.cache.Get(keys.PermissionKey(apiKey))
		if err != nil && err != gcache.KeyNotFoundError {
			return "", nil, err
		} else if err == nil {
			permissions, ok = perms.([]pkg.Permission)
			if !ok {
				return "", nil, errors.New("permission must be an array")
			}
		}
	}

	userID, ok = u.(string)
	if !ok {
		return "", nil, errors.New("invalid user id, expected string")
	}

	return userID, permissions, nil
}
