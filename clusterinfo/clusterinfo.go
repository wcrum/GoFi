// Package clusterinfo describes what a Kubernetes cluster offers for
// networking (Gateway API implementations, Istio and ambient mode, MetalLB,
// UDS Core, load balancers) and lists the objects that shape traffic.
// Everything here is read-only. Sections GoFi is not permitted to read become
// warnings rather than failures.
package clusterinfo

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/wcrum/gofi/kubeclient"
)

// Well-known controllers and the route kinds each implementation programs.
const (
	EnvoyGatewayController  = "gateway.envoyproxy.io/gatewayclass-controller"
	IstioGatewayController  = "istio.io/gateway-controller"
	IstioWaypointController = "istio.io/mesh-controller"
)

var routeKinds = map[string][]string{
	"envoy-gateway": {"HTTPRoute", "GRPCRoute", "TLSRoute", "TCPRoute", "UDPRoute"},
	"istio":         {"HTTPRoute", "GRPCRoute", "TLSRoute", "TCPRoute"},
}

// Options says what GoFi is configured to do with what it discovers.
type Options struct {
	Namespace string
	// ClusterWide means GoFi has a read-only ClusterRole for discovery.
	ClusterWide            bool
	ManageGatewayListeners bool
	ManageUDSAllows        bool
	NodePortMin            int
	NodePortMax            int
}

// Client reads cluster networking state.
type Client struct {
	CS   kubernetes.Interface
	Dyn  dynamic.Interface
	Disc discovery.DiscoveryInterface
	Opts Options

	mu        sync.Mutex
	versions  map[string]string          // API group -> preferred version
	all       map[string][]string        // API group -> versions, preferred first
	resources map[string]map[string]bool // group/version -> served resources
}

func New(cs kubernetes.Interface, dyn dynamic.Interface, opts Options) *Client {
	return &Client{CS: cs, Dyn: dyn, Disc: cs.Discovery(), Opts: opts}
}

// groupVersions maps each served API group to its preferred version.
func (c *Client) groupVersions() (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	groups, err := c.Disc.ServerGroups()
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	all := map[string][]string{}
	for _, group := range groups.Groups {
		versions[group.Name] = group.PreferredVersion.Version
		all[group.Name] = []string{group.PreferredVersion.Version}
		for _, v := range group.Versions {
			if v.Version != group.PreferredVersion.Version {
				all[group.Name] = append(all[group.Name], v.Version)
			}
		}
	}
	c.versions = versions
	c.all = all
	c.resources = map[string]map[string]bool{}
	return versions, nil
}

// versionFor is the first version of a group, preferred first, that serves
// a resource: kinds in one group can live at different versions (MetalLB
// prefers v1beta2 but serves IPAddressPools only at v1beta1).
func (c *Client) versionFor(group, resource string) string {
	c.mu.Lock()
	versions := append([]string(nil), c.all[group]...)
	c.mu.Unlock()
	for _, version := range versions {
		if c.served(group+"/"+version, resource) {
			return version
		}
	}
	return ""
}

// served reports whether a group/version serves a resource; a group can exist
// without every kind (e.g. Gateway API's standard channel has no TCPRoute).
func (c *Client) served(groupVersion, resource string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resources == nil {
		c.resources = map[string]map[string]bool{}
	}
	set, cached := c.resources[groupVersion]
	if !cached {
		set = map[string]bool{}
		if list, err := c.Disc.ServerResourcesForGroupVersion(groupVersion); err == nil {
			for _, r := range list.APIResources {
				set[r.Name] = true
			}
		}
		c.resources[groupVersion] = set
	}
	return set[resource]
}

// MaxListItems bounds every inventory listing. Listings page through the
// API and stop here; a truncated section is reported as a warning (the
// portal shows it) instead of failing or silently showing part of the cluster.
const MaxListItems = 2000

// list reads a resource in every namespace GoFi may read (or one namespace),
// paging through the API. It returns (nil, nil) when the API group is not
// served. A listing that stops at MaxListItems appends a warning.
func (c *Client) list(ctx context.Context, versions map[string]string, group, resource, namespace string, warnings *[]string) ([]unstructured.Unstructured, error) {
	version := c.versionFor(group, resource)
	if version == "" {
		return nil, nil
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if namespace == "" && !c.Opts.ClusterWide {
		namespace = c.Opts.Namespace
	}
	client := c.Dyn.Resource(gvr).Namespace(namespace)
	if namespace == "" {
		client = c.Dyn.Resource(gvr)
	}
	return c.pages(ctx, resource, client, warnings)
}

// listClusterScoped reads a cluster-scoped resource such as GatewayClasses.
func (c *Client) listClusterScoped(ctx context.Context, versions map[string]string, group, resource string, warnings *[]string) ([]unstructured.Unstructured, error) {
	version := c.versionFor(group, resource)
	if version == "" {
		return nil, nil
	}
	return c.pages(ctx, resource, c.Dyn.Resource(schema.GroupVersionResource{Group: group, Version: version, Resource: resource}), warnings)
}

// pages lists through a dynamic resource client up to MaxListItems.
func (c *Client) pages(ctx context.Context, resource string, client dynamic.ResourceInterface, warnings *[]string) ([]unstructured.Unstructured, error) {
	items, truncated, err := kubeclient.ListAll(ctx, MaxListItems, "", func(ctx context.Context, opts metav1.ListOptions) ([]unstructured.Unstructured, string, error) {
		ctx, cancel := context.WithTimeout(ctx, kubeclient.CallTimeout)
		defer cancel()
		list, err := client.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.GetContinue(), nil
	})
	if err != nil {
		return nil, err
	}
	if truncated && warnings != nil {
		*warnings = append(*warnings, kubeclient.TruncationWarning(resource, MaxListItems))
	}
	return items, nil
}

// listServices pages through the Services of a namespace ("" for all).
func (c *Client) listServices(ctx context.Context, namespace string, warnings *[]string) ([]corev1.Service, error) {
	items, truncated, err := kubeclient.ListAll(ctx, MaxListItems, "", func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Service, string, error) {
		ctx, cancel := context.WithTimeout(ctx, kubeclient.CallTimeout)
		defer cancel()
		list, err := c.CS.CoreV1().Services(namespace).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Continue, nil
	})
	if err != nil {
		return nil, err
	}
	if truncated && warnings != nil {
		*warnings = append(*warnings, kubeclient.TruncationWarning("Services", MaxListItems))
	}
	return items, nil
}

// listNamespaces pages through the cluster's namespaces.
func (c *Client) listNamespaces(ctx context.Context, warnings *[]string) ([]corev1.Namespace, error) {
	items, truncated, err := kubeclient.ListAll(ctx, MaxListItems, "", func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Namespace, string, error) {
		ctx, cancel := context.WithTimeout(ctx, kubeclient.CallTimeout)
		defer cancel()
		list, err := c.CS.CoreV1().Namespaces().List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Continue, nil
	})
	if err != nil {
		return nil, err
	}
	if truncated && warnings != nil {
		*warnings = append(*warnings, kubeclient.TruncationWarning("Namespaces", MaxListItems))
	}
	return items, nil
}

func warning(section string, err error) string {
	switch {
	case apierrors.IsForbidden(err):
		return section + ": not permitted (enable discovery.clusterWide in the chart)"
	default:
		return fmt.Sprintf("%s: %v", section, err)
	}
}

func str(obj map[string]any, path ...string) string {
	value, _, _ := unstructured.NestedString(obj, path...)
	return value
}

func slice(obj map[string]any, path ...string) []any {
	value, _, _ := unstructured.NestedSlice(obj, path...)
	return value
}

func conditionTrue(obj map[string]any, kind string) bool {
	for _, raw := range slice(obj, "status", "conditions") {
		if condition, ok := raw.(map[string]any); ok && condition["type"] == kind {
			return condition["status"] == "True"
		}
	}
	return false
}

func implementation(controller string) string {
	switch controller {
	case EnvoyGatewayController:
		return "envoy-gateway"
	case IstioGatewayController:
		return "istio"
	case IstioWaypointController:
		return "istio-waypoint"
	}
	return ""
}

func labelString(labels map[string]string) string {
	if len(labels) == 0 {
		return "all"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func stringMap(raw any) map[string]string {
	in, _ := raw.(map[string]any)
	out := map[string]string{}
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// imageTag returns the tag of the first container image, as a version hint.
func imageTag(image string) string {
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
		return zarfSuffix.ReplaceAllString(image[colon+1:], "")
	}
	return ""
}

// zarfSuffix is what Zarf appends to mirrored image tags, plus the
// distroless variant marker, neither of which is part of the version.
var zarfSuffix = regexp.MustCompile(`(-distroless)?(-zarf-\d+)?$`)
