package pkg

import "net"

type DeviceRegistry interface {
	Register(nodeId string, ip net.IP, os, version string, appVersion string) error
}
