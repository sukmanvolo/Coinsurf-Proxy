package cache

import (
	"errors"
	"github.com/VictoriaMetrics/fastcache"
	"github.com/alex023/clock"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"time"
)

type Options struct {
	OnExpired func(key string, value []byte)
}

type Option func(*Options)

func WithOnExpired(f func(key string, value []byte)) Option {
	return func(options *Options) {
		options.OnExpired = f
	}
}

type FastCache struct {
	clock   *clock.Clock
	cache   *fastcache.Cache
	options *Options
}

func NewFastCache(cache *fastcache.Cache, options ...Option) *FastCache {
	var opts = new(Options)
	for _, o := range options {
		o(opts)
	}

	return &FastCache{
		clock:   clock.NewClock(),
		cache:   cache,
		options: opts,
	}
}

func (m *FastCache) Get(key string, dst []byte) ([]byte, bool) {
	data := m.cache.Get(dst, zerocopy.Bytes(key))
	if len(data) == 0 {
		return nil, false
	}

	return data, true
}

func (m *FastCache) Delete(key string) {
	m.cache.Del(zerocopy.Bytes(key))
}

func (m *FastCache) SetWithExpire(key string, value []byte, expiration time.Duration) error {
	m.cache.Set(zerocopy.Bytes(key), value)

	_, ok := m.clock.AddJobWithDeadtime(time.Now().Add(expiration), func() {
		if m.options.OnExpired != nil {
			m.options.OnExpired(key, value)
		}

		m.Delete(key)
	})
	if !ok {
		return errors.New("failed to schedule cleanup")
	}

	return nil
}
