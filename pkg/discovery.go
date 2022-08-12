package pkg

type Discovery interface {
	Join() error
	Leave() error
}
