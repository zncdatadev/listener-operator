package util

import (
	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
)

type AddressInfo struct {
	Address     string
	AddressType listenersv1alpha1.AddressType
}

type ListenerIngress struct {
	AddressInfo
	Ports []listenersv1alpha1.PortSpec
}
