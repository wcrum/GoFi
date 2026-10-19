package clusterinfo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// component is a workload whose Pods identify an installed product.
type component struct {
	id, selector string
}

var components = []component{
	{"istiod", "app=istiod"},
	{"ztunnel", "app=ztunnel"},
	{"envoy-gateway", "control-plane=envoy-gateway"},
	{"metallb", "app.kubernetes.io/name=metallb"},
	{"metallb-legacy", "app=metallb"},
	{"k3s-servicelb", "svccontroller.k3s.cattle.io/svcname"},
	{"cilium", "k8s-app=cilium"},
	{"calico", "k8s-app=calico-node"},
	{"pepr", "pepr.dev/controller"},
}

// Describe reports capabilities, Gateways, address pools, and load balancers.
func (c *Client) Describe(ctx context.Context) (*gofiv1.DescribeClusterResponse, error) {
	out := &gofiv1.DescribeClusterResponse{Namespace: c.Opts.Namespace, ClusterWide: c.Opts.ClusterWide,
		ManageGatewayListeners: c.Opts.ManageGatewayListeners, ManageUdsAllows: c.Opts.ManageUDSAllows,
		NodePortMin: int32(c.Opts.NodePortMin), NodePortMax: int32(c.Opts.NodePortMax)}
	if info, err := c.Disc.ServerVersion(); err == nil {
		out.KubernetesVersion = info.GitVersion
	}
	versions, err := c.groupVersions()
	if err != nil {
		return nil, fmt.Errorf("API discovery: %w", err)
	}

	// Workloads that identify installed products (read-only Pod list).
	found := map[string][]corev1.Pod{}
	if c.Opts.ClusterWide {
		for _, comp := range components {
			pods, err := c.CS.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: comp.selector, Limit: 20})
			if err != nil {
				out.Warnings = append(out.Warnings, warning("Pods", err))
				break
			}
			found[comp.id] = pods.Items
		}
	}
	podVersion := func(id string) string {
		for _, pod := range found[id] {
			if len(pod.Spec.Containers) > 0 {
				return imageTag(pod.Spec.Containers[0].Image)
			}
		}
		return ""
	}
	where := func(id string) string {
		if pods := found[id]; len(pods) > 0 {
			return fmt.Sprintf("%d %s Pod(s) in %s", len(pods), id, pods[0].Namespace)
		}
		return ""
	}

	classes, err := c.listClusterScoped(ctx, versions, "gateway.networking.k8s.io", "gatewayclasses", &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("GatewayClasses", err))
	}
	classController := map[string]string{}
	for _, class := range classes {
		controller := str(class.Object, "spec", "controllerName")
		classController[class.GetName()] = controller
		out.GatewayClasses = append(out.GatewayClasses, &gofiv1.GatewayClassInfo{Name: class.GetName(), Controller: controller, Accepted: conditionTrue(class.Object, "Accepted"), Implementation: implementation(controller)})
	}
	hasController := func(controller string) []string {
		var names []string
		for name, value := range classController {
			if value == controller {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names
	}

	gateways, err := c.list(ctx, versions, "gateway.networking.k8s.io", "gateways", "", &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("Gateways", err))
	}
	for _, gw := range gateways {
		out.Gateways = append(out.Gateways, gatewayInfo(gw, classController))
	}
	sort.Slice(out.Gateways, func(i, j int) bool {
		return out.Gateways[i].Namespace+"/"+out.Gateways[i].Name < out.Gateways[j].Namespace+"/"+out.Gateways[j].Name
	})

	pools, err := c.list(ctx, versions, "metallb.io", "ipaddresspools", "", &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("IPAddressPools", err))
	}
	for _, pool := range pools {
		info := &gofiv1.AddressPoolInfo{Namespace: pool.GetNamespace(), Name: pool.GetName(), AutoAssign: true}
		for _, address := range slice(pool.Object, "spec", "addresses") {
			info.Addresses = append(info.Addresses, fmt.Sprint(address))
		}
		if auto, ok, _ := unstructured.NestedBool(pool.Object, "spec", "autoAssign"); ok {
			info.AutoAssign = auto
		}
		out.AddressPools = append(out.AddressPools, info)
	}

	lbClasses := map[string]bool{}
	serviceNamespace := ""
	if !c.Opts.ClusterWide {
		serviceNamespace = c.Opts.Namespace
	}
	services, err := c.listServices(ctx, serviceNamespace, &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("Services", err))
	} else {
		for _, svc := range services {
			if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
				continue
			}
			info := &gofiv1.LoadBalancerInfo{Namespace: svc.Namespace, Name: svc.Name}
			if svc.Spec.LoadBalancerClass != nil {
				info.ClassName = *svc.Spec.LoadBalancerClass
				lbClasses[info.ClassName] = true
			}
			for _, ingress := range svc.Status.LoadBalancer.Ingress {
				info.Addresses = append(info.Addresses, firstNonEmpty(ingress.IP, ingress.Hostname))
			}
			for _, port := range svc.Spec.Ports {
				info.Ports = append(info.Ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
			}
			out.LoadBalancers = append(out.LoadBalancers, info)
		}
	}
	for class := range lbClasses {
		out.LoadBalancerClasses = append(out.LoadBalancerClasses, class)
	}
	sort.Strings(out.LoadBalancerClasses)

	if c.Opts.ClusterWide {
		if list, err := c.listNamespaces(ctx, &out.Warnings); err != nil {
			out.Warnings = append(out.Warnings, warning("Namespaces", err))
		} else {
			for _, ns := range list {
				out.Namespaces = append(out.Namespaces, ns.Name)
			}
		}
	} else {
		out.Namespaces = []string{c.Opts.Namespace}
	}
	sort.Strings(out.Namespaces)

	// Capabilities.
	add := func(cap *gofiv1.ClusterCapability) { out.Capabilities = append(out.Capabilities, cap) }
	gatewayAPI := &gofiv1.ClusterCapability{Id: "gateway-api", Name: "Gateway API", Present: versions["gateway.networking.k8s.io"] != "", Version: versions["gateway.networking.k8s.io"]}
	if gatewayAPI.Present {
		gatewayAPI.Detail = fmt.Sprintf("%d GatewayClass(es), %d Gateway(s)", len(classes), len(gateways))
		gatewayAPI.Evidence = append(gatewayAPI.Evidence, "API group gateway.networking.k8s.io")
	}
	add(gatewayAPI)

	istio := &gofiv1.ClusterCapability{Id: "istio", Name: "Istio", Present: versions["networking.istio.io"] != "" || len(found["istiod"]) > 0, Version: podVersion("istiod")}
	if versions["networking.istio.io"] != "" {
		istio.Evidence = append(istio.Evidence, "API group networking.istio.io")
	}
	if w := where("istiod"); w != "" {
		istio.Evidence = append(istio.Evidence, w)
	}
	if names := hasController(IstioGatewayController); len(names) > 0 {
		istio.Evidence = append(istio.Evidence, "GatewayClass "+strings.Join(names, ", "))
	}
	add(istio)

	ambient := &gofiv1.ClusterCapability{Id: "istio-ambient", Name: "Istio ambient mode", Present: len(found["ztunnel"]) > 0 || len(hasController(IstioWaypointController)) > 0, Version: podVersion("ztunnel")}
	if w := where("ztunnel"); w != "" {
		ambient.Evidence = append(ambient.Evidence, w)
	}
	if ambient.Present {
		ambient.Detail = "Traffic between ambient workloads is mTLS through ztunnel; AuthorizationPolicies apply at L4."
	}
	add(ambient)

	envoy := &gofiv1.ClusterCapability{Id: "envoy-gateway", Name: "Envoy Gateway", Present: len(hasController(EnvoyGatewayController)) > 0 || versions["gateway.envoyproxy.io"] != "" || len(found["envoy-gateway"]) > 0, Version: podVersion("envoy-gateway")}
	if names := hasController(EnvoyGatewayController); len(names) > 0 {
		envoy.Evidence = append(envoy.Evidence, "GatewayClass "+strings.Join(names, ", "))
		envoy.Detail = "Supports TCPRoute and UDPRoute: GoFi can expose TCP and UDP flows through an Envoy Gateway listener."
	}
	if w := where("envoy-gateway"); w != "" {
		envoy.Evidence = append(envoy.Evidence, w)
	}
	add(envoy)

	metallb := &gofiv1.ClusterCapability{Id: "metallb", Name: "MetalLB", Present: versions["metallb.io"] != "", Version: firstNonEmpty(podVersion("metallb"), podVersion("metallb-legacy"))}
	if metallb.Present {
		metallb.Detail = fmt.Sprintf("%d IPAddressPool(s)", len(pools))
		metallb.Evidence = append(metallb.Evidence, "API group metallb.io")
	}
	add(metallb)

	lb := &gofiv1.ClusterCapability{Id: "load-balancer", Name: "LoadBalancer Services", Present: metallb.Present || len(found["k3s-servicelb"]) > 0 || len(out.LoadBalancers) > 0}
	assigned := 0
	for _, info := range out.LoadBalancers {
		if len(info.Addresses) > 0 {
			assigned++
		}
	}
	lb.Detail = fmt.Sprintf("%d LoadBalancer Service(s), %d with an assigned address", len(out.LoadBalancers), assigned)
	if metallb.Present {
		lb.Evidence = append(lb.Evidence, "MetalLB")
	}
	if len(found["k3s-servicelb"]) > 0 {
		lb.Evidence = append(lb.Evidence, "k3s ServiceLB")
	}
	if len(out.LoadBalancerClasses) > 0 {
		lb.Evidence = append(lb.Evidence, "classes: "+strings.Join(out.LoadBalancerClasses, ", "))
	}
	add(lb)

	uds := &gofiv1.ClusterCapability{Id: "uds-core", Name: "UDS Core", Present: versions["uds.dev"] != "", Version: podVersion("pepr")}
	if uds.Present {
		uds.Evidence = append(uds.Evidence, "API group uds.dev")
		uds.Detail = "UDS Packages generate default-deny NetworkPolicies and Istio AuthorizationPolicies; flows need explicit allows for traffic outside the GoFi namespace."
	}
	add(uds)

	netpol := &gofiv1.ClusterCapability{Id: "network-policy", Name: "NetworkPolicy enforcement", Present: true}
	switch {
	case len(found["cilium"]) > 0:
		netpol.Detail, netpol.Evidence = "Cilium enforces NetworkPolicies.", []string{where("cilium")}
	case len(found["calico"]) > 0:
		netpol.Detail, netpol.Evidence = "Calico enforces NetworkPolicies.", []string{where("calico")}
	case len(found["k3s-servicelb"]) > 0 || strings.Contains(out.KubernetesVersion, "k3s"):
		netpol.Detail = "k3s enforces NetworkPolicies with its embedded policy controller."
	default:
		netpol.Detail = "The API is available; enforcement depends on the CNI, which GoFi could not identify."
	}
	add(netpol)
	return out, nil
}

func gatewayInfo(gw unstructured.Unstructured, classController map[string]string) *gofiv1.GatewayInfo {
	className := str(gw.Object, "spec", "gatewayClassName")
	controller := classController[className]
	info := &gofiv1.GatewayInfo{Namespace: gw.GetNamespace(), Name: gw.GetName(), ClassName: className, Controller: controller,
		Programmed: conditionTrue(gw.Object, "Programmed"), Implementation: implementation(controller)}
	info.Waypoint = info.Implementation == "istio-waypoint"
	info.RouteKinds = routeKinds[info.Implementation]
	for _, raw := range slice(gw.Object, "status", "addresses") {
		if address, ok := raw.(map[string]any); ok {
			info.Addresses = append(info.Addresses, fmt.Sprint(address["value"]))
		}
	}
	attached := map[string]int32{}
	for _, raw := range slice(gw.Object, "status", "listeners") {
		if status, ok := raw.(map[string]any); ok {
			count, _ := status["attachedRoutes"].(int64)
			attached[fmt.Sprint(status["name"])] = int32(count)
		}
	}
	for _, raw := range slice(gw.Object, "spec", "listeners") {
		listener, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		port, _ := listener["port"].(int64)
		name := fmt.Sprint(listener["name"])
		info.Listeners = append(info.Listeners, &gofiv1.GatewayListenerInfo{Name: name, Protocol: fmt.Sprint(listener["protocol"]), Port: int32(port),
			Hostname: str(listener, "hostname"), AllowedRoutes: allowedRoutes(listener), AttachedRoutes: attached[name], ManagedByGofi: strings.HasPrefix(name, ListenerPrefix)})
	}
	return info
}

func allowedRoutes(listener map[string]any) string {
	from := str(listener, "allowedRoutes", "namespaces", "from")
	switch from {
	case "", "Same":
		return "Same"
	case "Selector":
		labels, _, _ := unstructured.NestedMap(listener, "allowedRoutes", "namespaces", "selector", "matchLabels")
		return "Selector: " + labelString(stringMap(labels))
	}
	return from
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ListenerPrefix marks Gateway listeners GoFi adds for flows.
const ListenerPrefix = "gofi-"
