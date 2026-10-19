package kubeclient

import "k8s.io/apimachinery/pkg/labels"

// selectorOf turns an equality map into a label selector (nil: everything).
func selectorOf(set map[string]string) labels.Selector {
	if len(set) == 0 {
		return labels.Everything()
	}
	return labels.SelectorFromSet(set)
}
