package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/spec"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// FlowSetReconciler is implemented by drivers that maintain objects derived
// from every flow at once. The controller calls it after each fresh sync.
type FlowSetReconciler interface {
	ReconcileFlowSet(ctx context.Context, defs []spec.FlowDefinition) error
}

// FlowAdmission is implemented by drivers that can check a flow against the
// platform before it is saved (for UDS, Pepr admission of GoFi's Package).
type FlowAdmission interface {
	AdmitFlow(ctx context.Context, def spec.FlowDefinition, others []spec.FlowDefinition) error
}

// AdmitFlow dry-runs GoFi's UDS Package with this flow's allows through the
// API server, so Pepr rejects an invalid allow when the flow is saved rather
// than on the next reconcile.
func (k *Kube) AdmitFlow(ctx context.Context, def spec.FlowDefinition, others []spec.FlowDefinition) error {
	if k.cluster == nil || !k.cluster.Opts.ManageUDSAllows || k.dyn == nil {
		return nil
	}
	desired, err := k.desiredUDSPackage(ctx, append(others, def))
	if err != nil {
		return nil // reported by the reconcile and the connectivity check
	}
	_, err = k.dyn.Resource(udsPackages).Namespace(k.namespace).Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{FieldManager: udsFieldManager, Force: true, DryRun: []string{metav1.DryRunAll}})
	if err != nil {
		return fmt.Errorf("UDS (Pepr) rejects this flow's network allows: %w", err)
	}
	return nil
}

// udsFieldManager is GoFi's server-side apply identity for the Package.
const udsFieldManager = "gofi"

var udsPackages = schema.GroupVersionResource{Group: "uds.dev", Version: "v1alpha1", Resource: "packages"}

// ReconcileFlowSet keeps the namespace's UDS Package in step with the flows.
func (k *Kube) ReconcileFlowSet(ctx context.Context, defs []spec.FlowDefinition) error {
	if k.cluster == nil || !k.cluster.Opts.ManageUDSAllows || k.dyn == nil {
		return nil
	}
	return k.reconcileUDSAllows(ctx, defs)
}

// reconcileUDSAllows owns the namespace's UDS Package: the base the chart
// ships in a ConfigMap (SSO, expose, monitors, static allows) plus the allows
// GoFi generates for flows. UDS allows one Package per namespace and the
// allow list is a single field, so the Package has exactly one owner: GoFi.
// Helm owns only the ConfigMap, and the Package is garbage-collected with it.
func (k *Kube) reconcileUDSAllows(ctx context.Context, defs []spec.FlowDefinition) error {
	desired, err := k.desiredUDSPackage(ctx, defs)
	if err != nil {
		return wrapUDS(err)
	}
	return k.applyUDSPackage(ctx, desired)
}

// applyUDSPackage makes desired GoFi's live Package.
func (k *Kube) applyUDSPackage(ctx context.Context, desired *unstructured.Unstructured) error {
	packages := k.dyn.Resource(udsPackages).Namespace(k.namespace)
	// Earlier GoFi versions created a separate gofi-flows Package when none
	// existed; UDS allows one per namespace, so remove it.
	if stale, err := packages.List(ctx, metav1.ListOptions{LabelSelector: kubeLabelManaged + "=true"}); err == nil {
		for _, item := range stale.Items {
			if item.GetName() != desired.GetName() {
				if err := packages.Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					return wrapUDS(err)
				}
			}
		}
	}
	if current, err := packages.Get(ctx, desired.GetName(), metav1.GetOptions{}); err == nil {
		owned := false
		for _, entry := range current.GetManagedFields() {
			owned = owned || entry.Manager == udsFieldManager && entry.Operation == metav1.ManagedFieldsOperationApply
		}
		if owned {
			if applied, err := k.dryRunUDSPackage(ctx, desired); err == nil && sameSpec(current, applied) {
				return nil
			}
		}
	}
	// Force takes the fields over once from whoever applied the Package before
	// (an earlier chart version rendered it directly).
	_, err := packages.Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{FieldManager: udsFieldManager, Force: true})
	return wrapUDS(err)
}

// desiredUDSPackage is the Package GoFi applies for a flow set: the chart's
// base from the ConfigMap plus the generated allows.
func (k *Kube) desiredUDSPackage(ctx context.Context, defs []spec.FlowDefinition) (*unstructured.Unstructured, error) {
	name := os.Getenv("GOFI_UDS_PACKAGE_BASE")
	if name == "" {
		return nil, fmt.Errorf("no Package base configured (set uds.manageAllows and gofiManagesPackage together)")
	}
	cm, err := k.cs.CoreV1().ConfigMaps(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Package base ConfigMap %q: %w", name, err)
	}
	desired, err := udsPackageFromBase(cm.Data["package.yaml"], k.namespace, UDSAllows(defs))
	if err != nil {
		return nil, err
	}
	desired.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: cm.Name, UID: cm.UID}})
	return desired, nil
}

// udsPackageFromBase builds the Package GoFi applies: the chart's base with
// the generated allows appended after its own.
func udsPackageFromBase(base, namespace string, generated []any) (*unstructured.Unstructured, error) {
	if strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("the Package base ConfigMap has no package.yaml")
	}
	var object map[string]any
	if err := yaml.Unmarshal([]byte(base), &object); err != nil {
		return nil, fmt.Errorf("Package base: %w", err)
	}
	pkg := &unstructured.Unstructured{Object: object}
	if pkg.GetKind() != "Package" || pkg.GetAPIVersion() != "uds.dev/v1alpha1" || pkg.GetName() == "" {
		return nil, fmt.Errorf("Package base must be a uds.dev/v1alpha1 Package with a name")
	}
	pkg.SetNamespace(namespace)
	allows, _, _ := unstructured.NestedSlice(pkg.Object, "spec", "network", "allow")
	var static []any
	for _, raw := range allows {
		if entry, ok := raw.(map[string]any); ok && strings.HasPrefix(fmt.Sprint(entry["description"]), udsEntryPrefix) {
			continue // generated entries are never part of the base
		}
		static = append(static, raw)
	}
	if err := unstructured.SetNestedSlice(pkg.Object, append(static, generated...), "spec", "network", "allow"); err != nil {
		return nil, err
	}
	return pkg, nil
}

// dryRunUDSPackage is desired as the API server would store it: with the
// CRD's defaults filled in (an SSO client gains enabled, publicClient, ...),
// so it compares equal to an unchanged live Package. The error is Pepr's (or
// the API server's) admission verdict.
func (k *Kube) dryRunUDSPackage(ctx context.Context, desired *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return k.dyn.Resource(udsPackages).Namespace(k.namespace).Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{FieldManager: udsFieldManager, Force: true, DryRun: []string{metav1.DryRunAll}})
}

// sameSpec compares two Packages' specs, ignoring int64/float differences.
func sameSpec(a, b *unstructured.Unstructured) bool {
	aSpec, _, _ := unstructured.NestedMap(a.Object, "spec")
	bSpec, _, _ := unstructured.NestedMap(b.Object, "spec")
	return normalizeJSON([]any{aSpec}) == normalizeJSON([]any{bSpec})
}

// udsEntryPrefix marks the allow entries GoFi generates (by description).
const udsEntryPrefix = "GoFi flow "

// normalizeJSON makes values comparable between API (int64) and local types.
func normalizeJSON(items []any) string {
	data, _ := json.Marshal(items)
	return string(data)
}

func wrapUDS(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("UDS network allows: %w", err)
}

// UDSAllows derives the UDS allow entries every flow needs: ingress for its
// exposure and egress for destinations outside the GoFi namespace. Traffic
// inside the namespace is already allowed by GoFi's main Package. Entries are
// sorted so an unchanged flow set produces an identical Package.
func UDSAllows(defs []spec.FlowDefinition) []any {
	var allows []map[string]any
	for _, def := range defs {
		selector := map[string]any{kubeLabelListener: def.Name}
		if listen := def.Source.Listen; listen != nil && listen.Service != nil {
			protocol := strings.ToUpper(listen.Network)
			if protocol == "" {
				protocol = "TCP"
			}
			entry := map[string]any{"direction": "Ingress", "selector": selector, "port": int64(listen.Port), "remoteProtocol": protocol}
			switch listen.Service.Type {
			case "NodePort", "LoadBalancer":
				entry["remoteGenerated"] = "Anywhere"
				entry["description"] = fmt.Sprintf("GoFi flow %s %s ingress", def.Name, listen.Service.Type)
			case "Gateway":
				if gw := listen.Service.Gateway; gw != nil {
					entry["remoteNamespace"] = gw.Namespace
					entry["description"] = fmt.Sprintf("GoFi flow %s ingress from Gateway %s/%s", def.Name, gw.Namespace, gw.Name)
				}
			default: // ClusterIP: chosen senders, or anywhere in the cluster
				if len(listen.Service.AllowFrom) > 0 {
					for _, peer := range listen.Service.AllowFrom {
						from := map[string]any{"direction": "Ingress", "selector": selector, "port": int64(listen.Port), "remoteProtocol": protocol, "remoteNamespace": peer.Namespace,
							"description": fmt.Sprintf("GoFi flow %s ingress from %s", def.Name, peerName(peer))}
						if len(peer.Selector) > 0 {
							remote := map[string]any{}
							for k, v := range peer.Selector {
								remote[k] = v
							}
							from["remoteSelector"] = remote
						}
						allows = append(allows, from)
					}
					entry = nil // replaced by one entry per sender
					break
				}
				entry["remoteNamespace"] = "*"
				entry["description"] = fmt.Sprintf("GoFi flow %s in-cluster ingress", def.Name)
			}
			if entry != nil {
				allows = append(allows, entry)
			}
		}
		if entry := sourceEgress(def.Name, def.Source); entry != nil {
			entry["selector"] = selector
			allows = append(allows, entry)
		}
		sinks := append([]spec.SinkCfg{}, def.Sinks...)
		if def.DeadLetterSink != nil {
			sinks = append(sinks, *def.DeadLetterSink)
		}
		for _, sink := range sinks {
			if entry := egressAllow(def.Name, sink); entry != nil {
				entry["selector"] = selector
				allows = append(allows, entry)
			}
		}
	}
	sort.SliceStable(allows, func(i, j int) bool {
		return fmt.Sprint(allows[i]["description"]) < fmt.Sprint(allows[j]["description"])
	})
	out := make([]any, 0, len(allows))
	seen := map[string]bool{}
	for _, entry := range allows {
		key := fmt.Sprint(entry)
		if !seen[key] {
			seen[key] = true
			out = append(out, entry)
		}
	}
	return out
}

func peerName(peer spec.PeerRef) string {
	if peer.Package != "" {
		return peer.Namespace + "/" + peer.Package
	}
	return peer.Namespace
}

// egressAllow is the egress a destination outside the GoFi namespace needs,
// or nil when none is needed (in-namespace targets, stdout, Python).
func egressAllow(flow string, sink spec.SinkCfg) map[string]any {
	protocol := "TCP"
	if sink.Type == "udp" {
		protocol = "UDP"
	}
	if target := sink.Kubernetes; target != nil {
		if target.Namespace == "" {
			return nil
		}
		selector := map[string]any{}
		for k, v := range target.Selector {
			selector[k] = v
		}
		return map[string]any{"direction": "Egress", "remoteNamespace": target.Namespace, "remoteSelector": selector, "port": int64(target.Port), "remoteProtocol": protocol,
			"description": fmt.Sprintf("GoFi flow %s to %s pods", flow, target.Namespace)}
	}
	return urlEgress(fmt.Sprintf("GoFi flow %s", flow), sink.URL, protocol)
}

// defaultPorts are the ports implied by URL schemes GoFi connects to.
var defaultPorts = map[string]int{"http": 80, "https": 443, "ws": 80, "wss": 443, "nats": 4222, "tls": 4222, "tcp": 1883, "ssl": 8883, "mqtt": 1883, "mqtts": 8883}

// tlsSchemes carry TLS, so UDS can allow them by host name (remoteHost).
var tlsSchemes = map[string]bool{"https": true, "wss": true, "tls": true, "ssl": true, "mqtts": true}

// urlEgress is the allow for an outbound connection to rawURL: a namespace
// for in-cluster Service DNS, a CIDR for an IP, a host for TLS or HTTP, and
// otherwise any address on the port (UDS cannot match plain TCP or UDP by name).
func urlEgress(prefix, rawURL, protocol string) map[string]any {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	host := parsed.Hostname()
	port, _ := strconv.Atoi(parsed.Port())
	if port == 0 {
		port = defaultPorts[parsed.Scheme]
	}
	description := prefix + " to " + host
	entry := map[string]any{"direction": "Egress", "remoteProtocol": protocol, "description": description}
	if port != 0 {
		entry["port"] = int64(port)
	}
	if labels := strings.Split(host, "."); len(labels) >= 3 && labels[2] == "svc" {
		entry["remoteNamespace"] = labels[1]
		return entry
	}
	if ip := net.ParseIP(host); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		entry["remoteCidr"] = fmt.Sprintf("%s/%d", ip, bits)
		return entry
	}
	if !strings.Contains(host, ".") {
		return nil // a bare name resolves inside the GoFi namespace
	}
	switch {
	case protocol == "TCP" && tlsSchemes[parsed.Scheme]:
		entry["remoteHost"], entry["remoteProtocol"] = host, "TLS"
	case protocol == "TCP" && (parsed.Scheme == "http" || parsed.Scheme == "ws"):
		entry["remoteHost"], entry["remoteProtocol"] = host, "HTTP"
	default:
		entry["remoteGenerated"] = "Anywhere"
	}
	return entry
}

// sourceEgress is the allow a fetch or subscribe source needs.
func sourceEgress(flow string, source spec.FlowSource) map[string]any {
	prefix := fmt.Sprintf("GoFi flow %s source", flow)
	switch {
	case source.Fetch != nil && source.Fetch.HTTP != nil:
		return urlEgress(prefix, source.Fetch.HTTP.URL, "TCP")
	case source.Subscribe != nil && source.Subscribe.NATS != nil:
		return urlEgress(prefix, source.Subscribe.NATS.URL, "TCP")
	case source.Subscribe != nil && source.Subscribe.MQTT != nil:
		return urlEgress(prefix, source.Subscribe.MQTT.URL, "TCP")
	case source.Subscribe != nil && source.Subscribe.WebSocket != nil:
		return urlEgress(prefix, source.Subscribe.WebSocket.URL, "TCP")
	case source.Subscribe != nil && source.Subscribe.SSE != nil:
		return urlEgress(prefix, source.Subscribe.SSE.URL, "TCP")
	}
	return nil
}

// PackageRefusal is an ApplyUDSPackage GoFi will not perform: it does not
// manage its Package, or the reviewed plan is stale.
type PackageRefusal struct{ Reason string }

func (e *PackageRefusal) Error() string { return e.Reason }

// PlanUDSPackage compares GoFi's Package as it is live with the Package GoFi
// would apply for flows, and dry-runs the latter through Pepr admission.
func (k *Kube) PlanUDSPackage(ctx context.Context, flows []spec.FlowDefinition) *gofiv1.PlanUDSPackageResponse {
	plan, _ := k.planUDSPackage(ctx, flows)
	return plan
}

// planUDSPackage is PlanUDSPackage plus the desired object it describes (nil
// when there is no plan).
func (k *Kube) planUDSPackage(ctx context.Context, flows []spec.FlowDefinition) (*gofiv1.PlanUDSPackageResponse, *unstructured.Unstructured) {
	plan := &gofiv1.PlanUDSPackageResponse{Namespace: k.namespace, Managed: k.cluster != nil && k.cluster.Opts.ManageUDSAllows}
	if k.dyn == nil {
		plan.Error = "UDS Packages need the Kubernetes dynamic client"
		return plan, nil
	}
	desired, err := k.desiredUDSPackage(ctx, flows)
	if err != nil {
		plan.Error = err.Error()
		return plan, nil
	}
	plan.Name = desired.GetName()
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	plan.DesiredYaml = specYAML(desiredSpec)
	sum := sha256.Sum256([]byte(normalizeJSON([]any{desiredSpec})))
	plan.Digest = hex.EncodeToString(sum[:8])
	live, err := k.dyn.Resource(udsPackages).Namespace(k.namespace).Get(ctx, desired.GetName(), metav1.GetOptions{})
	switch {
	case err == nil:
		liveSpec, _, _ := unstructured.NestedMap(live.Object, "spec")
		plan.LiveYaml = specYAML(liveSpec)
	case apierrors.IsNotFound(err):
		live = nil
	default:
		plan.Error = fmt.Sprintf("read Package %s/%s: %v", k.namespace, desired.GetName(), err)
		return plan, nil
	}
	// Compare and show the Package as the server would store it, defaults
	// included, so the diff holds only real changes.
	plan.Admission = &gofiv1.DryRunResult{Accepted: true}
	if applied, err := k.dryRunUDSPackage(ctx, desired); err != nil {
		plan.Admission = &gofiv1.DryRunResult{Error: err.Error()}
	} else {
		appliedSpec, _, _ := unstructured.NestedMap(applied.Object, "spec")
		plan.DesiredYaml = specYAML(appliedSpec)
		plan.InSync = live != nil && sameSpec(live, applied)
	}
	return plan, desired
}

// ApplyUDSPackage applies exactly the Package a plan with digest described.
// It writes only GoFi's own Package, only when GoFi manages its allows, and
// only while that plan is still current. flows must be the reconcile loop's
// valid flow set, held stable by the caller (Controller.WithFlowSet).
func (k *Kube) ApplyUDSPackage(ctx context.Context, flows []spec.FlowDefinition, digest string) (*gofiv1.PlanUDSPackageResponse, error) {
	if k.cluster == nil || !k.cluster.Opts.ManageUDSAllows {
		return nil, &PackageRefusal{Reason: "GoFi does not manage its UDS Package (uds.manageAllows is off)"}
	}
	plan, desired := k.planUDSPackage(ctx, flows)
	switch {
	case plan.Error != "":
		return nil, errors.New(plan.Error)
	case digest == "" || digest != plan.Digest:
		return nil, &PackageRefusal{Reason: "GoFi's Package changed since it was reviewed; review the change again"}
	case plan.Admission != nil && !plan.Admission.Accepted:
		return nil, &PackageRefusal{Reason: "Pepr admission rejects GoFi's Package: " + plan.Admission.Error}
	}
	if err := k.applyUDSPackage(ctx, desired); err != nil {
		return nil, wrapUDS(err)
	}
	return k.PlanUDSPackage(ctx, flows), nil
}

func specYAML(value map[string]any) string {
	out, err := yaml.Marshal(map[string]any{"spec": value})
	if err != nil {
		return ""
	}
	return string(out)
}
