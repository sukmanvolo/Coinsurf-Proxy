package pkg

import "context"

type Accountant interface {
	//Decrement - decrement number of data user has
	Decrement(ctx context.Context, userID string, ownerId string, bytes int64) error

	//AccountBytes - returns number of bytes to take into account while data transfer
	AccountBytes() int64
}
