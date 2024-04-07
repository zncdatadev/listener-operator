package util

func ListenerLabelsForPod(listenerClass, listenerName string) map[string]string {
	return map[string]string{
		ListenersZncdataListenerClass: listenerClass,
		ListenersZncdataListenerName:  listenerName,
	}
}
