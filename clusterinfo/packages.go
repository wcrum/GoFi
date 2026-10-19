package clusterinfo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Packages lists UDS Packages as connection endpoints: what each exposes
// through a UDS gateway and which ingress its allow entries admit.
func (c *Client) Packages(ctx context.Context) (*gofiv1.ListUDSPackagesResponse, error) {
	versions, err := c.groupVersions()
	if err != nil {
		return nil, fmt.Errorf("API discovery: %w", err)
	}
	out := &gofiv1.ListUDSPackagesResponse{Namespace: c.Opts.Namespace}
	items, err := c.list(ctx, versions, "uds.dev", "packages", "", &out.Warnings)
	if err != nil {
		out.Warnings = append(out.Warnings, warning("UDS Packages", err))
		return out, nil
	}
	for _, item := range items {
		out.Packages = append(out.Packages, packageInfo(item))
	}
	sort.Slice(out.Packages, func(i, j int) bool {
		return out.Packages[i].Namespace+"/"+out.Packages[i].Name < out.Packages[j].Namespace+"/"+out.Packages[j].Name
	})
	return out, nil
}

func packageInfo(item unstructured.Unstructured) *gofiv1.UDSPackageInfo {
	info := &gofiv1.UDSPackageInfo{Namespace: item.GetNamespace(), Name: item.GetName(), Phase: str(item.Object, "status", "phase")}
	workloads := map[string]bool{}
	note := func(selector map[string]string) {
		if len(selector) > 0 {
			workloads[labelString(selector)] = true
		}
	}
	for _, raw := range slice(item.Object, "spec", "network", "expose") {
		entry, _ := raw.(map[string]any)
		selector := stringMap(entry["selector"])
		note(selector)
		port, _ := entry["port"].(int64)
		gateway := firstNonEmpty(str(entry, "gateway"), "tenant")
		info.Endpoints = append(info.Endpoints, &gofiv1.UDSPackageEndpoint{Kind: "expose", Description: str(entry, "description"), Selector: selector, Port: int32(port), Protocol: "TCP",
			Service: str(entry, "service"), Host: fmt.Sprintf("%s (%s gateway)", str(entry, "host"), gateway)})
	}
	for _, raw := range slice(item.Object, "spec", "network", "allow") {
		entry, _ := raw.(map[string]any)
		selector := stringMap(entry["selector"])
		note(selector)
		if !strings.EqualFold(str(entry, "direction"), "Ingress") {
			continue
		}
		remote := firstNonEmpty(str(entry, "remoteGenerated"), str(entry, "remoteNamespace"), str(entry, "remoteCidr"), "any")
		if r := stringMap(entry["remoteSelector"]); len(r) > 0 {
			remote += " " + labelString(r)
		}
		ports := []int32{}
		if port, ok := entry["port"].(int64); ok {
			ports = append(ports, int32(port))
		}
		for _, p := range slice(entry, "ports") {
			if port, ok := p.(int64); ok {
				ports = append(ports, int32(port))
			}
		}
		if len(ports) == 0 {
			ports = []int32{0}
		}
		for _, port := range ports {
			info.Endpoints = append(info.Endpoints, &gofiv1.UDSPackageEndpoint{Kind: "allow", Description: str(entry, "description"), Selector: selector, Port: port,
				Protocol: firstNonEmpty(strings.ToUpper(str(entry, "remoteProtocol")), "TCP"), Remote: remote})
		}
	}
	for _, raw := range slice(item.Object, "spec", "monitor") {
		entry, _ := raw.(map[string]any)
		note(stringMap(entry["selector"]))
	}
	for workload := range workloads {
		info.Workloads = append(info.Workloads, workload)
	}
	sort.Strings(info.Workloads)
	return info
}
