// Package invariants states what a converged GoFi deployment looks like and
// checks an observed world against the declared flow set. Observers (one per
// deployment shape: flow file, controller API, Docker daemon, Kubernetes API)
// fill a World; Check is pure and the same for every layer of the test
// pyramid (docs/testing.md).
//
// Invariants:
//
//	one-runtime        every running flow has exactly one healthy runtime
//	stopped-no-runtime a stopped flow has no runtime
//	no-orphan          no runtime, Service, Secret, NetworkPolicy, or worker
//	                   without a desired owner
//	service-shape      a flow Service's type, ports, selector match the flow
//	history            revision numbers are strictly monotonic and every
//	                   revision's hash matches its definition; the latest
//	                   live revision matches the stored flow
//	bindings           Python worker tokens in runtimes match the workers' Secrets
//	uds-allows         GoFi's UDS Package allows equal the generated set
//	metrics            every metrics endpoint answers
//	readyz             /readyz is truthful: ready implies the API answers
package invariants

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wcrum/gofi/driver"
	"github.com/wcrum/gofi/history"
	"github.com/wcrum/gofi/spec"
)

// Runtime is one observed flow runtime (goroutine, container, or Pod).
type Runtime struct {
	Flow   string
	ID     string
	Status string // running | pending | exited | ...; see Healthy
	Node   string
	// Def is the definition the runtime was started with, when observable
	// (FLOW_SPEC of a container or Pod).
	Def *spec.FlowDefinition
}

// Healthy reports whether the runtime counts as serving. Pending counts when
// pendingOK (envtest Pods never leave Pending: there is no kubelet).
func (r Runtime) Healthy(pendingOK bool) bool {
	switch strings.ToLower(r.Status) {
	case "running", "ready", "created":
		return true
	case "pending":
		return pendingOK
	}
	return false
}

// ServicePort is one port of an observed Service.
type ServicePort struct {
	Port, TargetPort, NodePort int32
	Protocol                   string
}

// Service is an observed flow Service.
type Service struct {
	Name     string
	Flow     string // the gofi.io/listener label
	Type     string
	Ports    []ServicePort
	Selector map[string]string
}

// Secret is an observed Python worker Secret (gofi.sandbox label).
type Secret struct {
	Name    string
	Library string
	Token   string
}

// Revision is an observed history revision.
type Revision struct {
	Number  int64
	Hash    string
	Deleted bool
	Def     *spec.FlowDefinition
}

// World is everything an observer could see.
type World struct {
	// Desired is the declared flow set, including stopped flows and
	// excluding definitions that fail validation.
	Desired []spec.FlowDefinition
	// Invalid names stored flows that do not validate (their last good
	// version may still run).
	Invalid []string
	// Libraries names the desired Python libraries (Sandbox resources).
	Libraries []string
	Runtimes  []Runtime
	Services  []Service
	Secrets   []Secret
	// Bindings is flow -> library -> token as the runtime received them.
	Bindings map[string]map[string]string
	// History is flow -> revisions, oldest first.
	History map[string][]Revision
	// Allows is the UDS Package's allow list; nil when not observed.
	Allows []any
	// Orphans lists objects an observer found with GoFi's ownership labels
	// but no owner among Desired/Libraries (kind/name).
	Orphans []string
	// Metrics is endpoint -> answered.
	Metrics map[string]bool
	// Ready is /readyz's answer; APIAnswers whether ListFlows worked.
	Ready, APIAnswers *bool
	// Observed names the observer (for reports).
	Observed string
}

// Options tune Check.
type Options struct {
	// PendingIsHealthy accepts Pending runtimes (envtest, cold k3d nodes).
	PendingIsHealthy bool
	// HistoryExpected requires every desired flow to have history; set when
	// the recorder had time to run.
	HistoryExpected bool
	// UDSExpected requires Allows to be observed and to match.
	UDSExpected bool
	// RuntimesExpected is false when the observer cannot see runtimes
	// (flow-file observer): runtime rules are skipped.
	RuntimesExpected bool
}

// Violation is one broken invariant.
type Violation struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

func (v Violation) String() string { return v.Rule + " " + v.Subject + ": " + v.Detail }

// Check returns every violation (nil when the world is converged).
func Check(w World, opts Options) []Violation {
	var out []Violation
	add := func(rule, subject, format string, args ...any) {
		out = append(out, Violation{Rule: rule, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	desired := map[string]spec.FlowDefinition{}
	invalid := map[string]bool{}
	for _, name := range w.Invalid {
		invalid[name] = true
	}
	for _, def := range w.Desired {
		desired[def.Name] = def
	}

	if opts.RuntimesExpected {
		byFlow := map[string][]Runtime{}
		for _, r := range w.Runtimes {
			byFlow[r.Flow] = append(byFlow[r.Flow], r)
		}
		for name, def := range desired {
			runtimes := byFlow[name]
			if def.Stopped() {
				if len(runtimes) != 0 {
					add("stopped-no-runtime", name, "%d runtime(s) for a stopped flow: %s", len(runtimes), ids(runtimes))
				}
				continue
			}
			healthy := 0
			for _, r := range runtimes {
				if r.Healthy(opts.PendingIsHealthy) {
					healthy++
				}
			}
			switch {
			case len(runtimes) == 0:
				add("one-runtime", name, "no runtime")
			case healthy != 1:
				add("one-runtime", name, "%d healthy of %d runtime(s): %s", healthy, len(runtimes), statuses(runtimes))
			}
			for _, r := range runtimes {
				if r.Def != nil && !sameRuntimeDefinition(*r.Def, def) {
					add("one-runtime", name, "runtime %s runs a different definition than desired", r.ID)
				}
			}
		}
		for flow, runtimes := range byFlow {
			if _, want := desired[flow]; !want && !invalid[flow] {
				add("no-orphan", "runtime/"+flow, "%d runtime(s) without a desired flow: %s", len(runtimes), ids(runtimes))
			}
		}
	}

	for _, orphan := range w.Orphans {
		add("no-orphan", orphan, "no desired owner")
	}

	servicesByFlow := map[string][]Service{}
	for _, svc := range w.Services {
		servicesByFlow[svc.Flow] = append(servicesByFlow[svc.Flow], svc)
	}
	for flow, services := range servicesByFlow {
		def, want := desired[flow]
		if !want {
			add("no-orphan", "service/"+flow, "%d Service(s) without a desired flow", len(services))
			continue
		}
		listen := def.Source.Listening()
		if listen.Service == nil || def.Stopped() {
			add("service-shape", flow, "Service exists but the flow declares no exposure (or is stopped)")
			continue
		}
		if len(services) != 1 {
			add("service-shape", flow, "%d Services, want 1", len(services))
			continue
		}
		svc := services[0]
		cfg := listen.Service
		if svc.Type != cfg.Type {
			add("service-shape", flow, "type %s, want %s", svc.Type, cfg.Type)
		}
		wantPort := int32(cfg.Port)
		if wantPort == 0 {
			wantPort = int32(listen.Port)
		}
		wantProto := "TCP"
		if listen.Network == "udp" || listen.Network == "quic" {
			wantProto = "UDP" // QUIC rides on UDP
		}
		if len(svc.Ports) != 1 {
			add("service-shape", flow, "%d ports, want 1", len(svc.Ports))
		} else {
			p := svc.Ports[0]
			if p.Port != wantPort || p.TargetPort != int32(listen.Port) || !strings.EqualFold(p.Protocol, wantProto) {
				add("service-shape", flow, "port %d->%d/%s, want %d->%d/%s", p.Port, p.TargetPort, p.Protocol, wantPort, listen.Port, wantProto)
			}
			if cfg.NodePort != 0 && p.NodePort != int32(cfg.NodePort) {
				add("service-shape", flow, "nodePort %d, want %d", p.NodePort, cfg.NodePort)
			}
		}
		if svc.Selector["gofi.io/listener"] != flow || svc.Selector["gofi.io/managed-by"] != "true" {
			add("service-shape", flow, "selector %v does not select the flow's runtime", svc.Selector)
		}
	}
	if opts.RuntimesExpected {
		for name, def := range desired {
			if def.Source.Listening().Service != nil && !def.Stopped() && len(servicesByFlow[name]) == 0 && len(w.Services) > 0 {
				add("service-shape", name, "flow declares a %s Service but none exists", def.Source.Listening().Service.Type)
			}
		}
	}

	for flow, revs := range w.History {
		var last int64
		for i, rev := range revs {
			if rev.Number <= last {
				add("history", flow, "revision %d after %d is not monotonic", rev.Number, last)
			}
			if i > 0 && rev.Number != last+1 && i != 0 {
				// Gaps are only legal at the start (retention prunes the oldest).
				add("history", flow, "revision gap %d -> %d", last, rev.Number)
			}
			last = rev.Number
			if rev.Deleted {
				if rev.Hash != "" || rev.Def != nil {
					add("history", flow, "tombstone revision %d carries a hash or definition", rev.Number)
				}
				continue
			}
			if rev.Def != nil {
				def := *rev.Def
				def.Name = flow
				if got := history.Hash(def); got != rev.Hash {
					add("history", flow, "revision %d hash %s does not match its definition (%s)", rev.Number, short(rev.Hash), short(got))
				}
			}
		}
		if len(revs) == 0 {
			continue
		}
		head := revs[len(revs)-1]
		def, want := desired[flow]
		switch {
		case want && head.Deleted:
			add("history", flow, "flow exists but its latest revision %d is a tombstone", head.Number)
		case want && !invalid[flow] && head.Hash != "" && history.Hash(def) != head.Hash:
			add("history", flow, "latest revision %d hash %s differs from the stored flow %s", head.Number, short(head.Hash), short(history.Hash(def)))
		case !want && !invalid[flow] && !head.Deleted && opts.HistoryExpected:
			add("history", flow, "flow is gone but its latest revision %d is not a tombstone", head.Number)
		}
	}
	if opts.HistoryExpected {
		for name := range desired {
			if len(w.History[name]) == 0 {
				add("history", name, "no revisions recorded")
			}
		}
	}

	if len(w.Bindings) > 0 {
		tokens := map[string]string{}
		for _, s := range w.Secrets {
			tokens[s.Library] = s.Token
		}
		for flow, libs := range w.Bindings {
			for lib, token := range libs {
				want, ok := tokens[lib]
				switch {
				case !ok && len(w.Secrets) > 0:
					add("bindings", flow, "library %s has no worker Secret", lib)
				case ok && want != token:
					add("bindings", flow, "library %s token differs from its Secret", lib)
				}
			}
		}
	}
	if len(w.Libraries) > 0 {
		libs := map[string]bool{}
		for _, lib := range w.Libraries {
			libs[lib] = true
		}
		for _, s := range w.Secrets {
			if !libs[s.Library] {
				add("no-orphan", "secret/"+s.Name, "worker Secret for undeclared library %s", s.Library)
			}
		}
	}

	if opts.UDSExpected {
		if w.Allows == nil {
			add("uds-allows", "package", "no UDS Package observed")
		} else {
			want := normalizeAllows(driver.UDSAllows(w.Desired))
			got := map[string]bool{}
			for _, entry := range normalizeAllows(w.Allows) {
				got[entry] = true
			}
			for _, entry := range want {
				if !got[entry] {
					add("uds-allows", "package", "missing generated allow %s", entry)
				}
			}
			wantSet := map[string]bool{}
			for _, entry := range want {
				wantSet[entry] = true
			}
			for entry := range got {
				if strings.Contains(entry, `"description":"GoFi flow `) && !wantSet[entry] {
					add("uds-allows", "package", "stale generated allow %s", entry)
				}
			}
		}
	}

	for endpoint, ok := range w.Metrics {
		if !ok {
			add("metrics", endpoint, "metrics endpoint did not answer")
		}
	}
	if w.Ready != nil && w.APIAnswers != nil {
		if *w.Ready && !*w.APIAnswers {
			add("readyz", "controller", "/readyz is 200 but the API does not answer")
		}
		if !*w.Ready && *w.APIAnswers {
			add("readyz", "controller", "/readyz is not 200 while the API answers and flows are listed")
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// sameRuntimeDefinition compares what a runtime runs with what is desired,
// ignoring the fields the controller adds or strips on the way (derived
// Python libraries, run state, test cases).
func sameRuntimeDefinition(running, desired spec.FlowDefinition) bool {
	running.State, desired.State = "", ""
	running.TestCases, desired.TestCases = nil, nil
	running.Name = desired.Name
	return history.Hash(running) == history.Hash(desired)
}

func normalizeAllows(allows []any) []string {
	out := make([]string, 0, len(allows))
	for _, entry := range allows {
		data, _ := json.Marshal(entry) // map keys marshal sorted
		out = append(out, string(data))
	}
	sort.Strings(out)
	return out
}

func ids(runtimes []Runtime) string {
	parts := make([]string, 0, len(runtimes))
	for _, r := range runtimes {
		parts = append(parts, r.ID)
	}
	return strings.Join(parts, ",")
}

func statuses(runtimes []Runtime) string {
	parts := make([]string, 0, len(runtimes))
	for _, r := range runtimes {
		parts = append(parts, r.ID+"="+r.Status)
	}
	return strings.Join(parts, ",")
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// Summary renders violations for a test failure message.
func Summary(violations []Violation) string {
	if len(violations) == 0 {
		return "all invariants hold"
	}
	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, v.String())
	}
	return fmt.Sprintf("%d invariant violation(s):\n  %s", len(violations), strings.Join(lines, "\n  "))
}
