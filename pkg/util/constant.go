package util

import (
	"fmt"
	"strings"

	listener "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
)

const (
	LabelListenerName      = "listeners.kubecoop.dev/listenerName"
	LabelListenerNamespace = "listeners.kubedoop.dev/listenerNamespace"
	LabelListenerMntPrefix = "listeners.kubedoop.dev/mnt."
)

func ListenerMountPodLabels(listener *listener.Listener) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s%s", LabelListenerMntPrefix, strings.ReplaceAll(string(listener.UID), "-", "")): listener.Name,
	}
}

func ListenerMetaLabels(listener *listener.Listener) map[string]string {
	return map[string]string{
		LabelListenerName:      listener.Name,
		LabelListenerNamespace: listener.Namespace,
	}
}
