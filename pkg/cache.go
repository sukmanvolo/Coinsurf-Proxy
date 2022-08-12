package pkg

import "time"

type Cache interface {
	Get(key string, dst []byte) ([]byte, bool)
	Delete(key string)
	SetWithExpire(key string, value []byte, expiration time.Duration) error
}
