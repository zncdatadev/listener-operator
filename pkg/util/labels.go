package util

import "github.com/zncdatadev/operator-go/pkg/constants"

func ListenerLabelsForPod(listenerClass, listenerName string) map[string]string {
	return map[string]string{
		constants.AnnotationListenersClass: listenerClass,
		constants.AnnotationListenerName:   listenerName,
	}
}
