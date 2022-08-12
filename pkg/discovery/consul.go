package discovery

import (
	"github.com/hashicorp/consul/api"
	"sync"
)

//Consul -
type Consul struct {
	mu sync.RWMutex

	serviceDef *api.AgentServiceRegistration
	client     *api.Client
}

func NewConsul(client *api.Client, serviceDef *api.AgentServiceRegistration) *Consul {
	return &Consul{serviceDef: serviceDef, client: client}
}

func (c *Consul) Join() error {
	return c.client.Agent().ServiceRegister(c.serviceDef)
}

func (c *Consul) Leave() error {
	return c.client.Agent().ServiceDeregister(c.serviceDef.ID)
}
