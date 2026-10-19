package kubeclient

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// PodDefaults are the chart's defaults for the Pods the controller creates
// (docs/deployment.md "Pod defaults from the chart"). The chart renders them
// as JSON in GOFI_<PREFIX>_RESOURCES, _SCHEDULING, _PROBES, and _ENV;
// operators tune them in values.yaml, never per flow or per library.
type PodDefaults struct {
	Resources  corev1.ResourceRequirements
	Scheduling Scheduling
	Probes     Probes
	// Env is appended to the main container of every Pod (chart:
	// listenerPods.extraEnv, workers.extraEnv, and GOFI_PROFILE from
	// .profile). Names the driver sets itself (FLOW_SPEC, GOFI_K8S_NAMESPACE,
	// the worker's source and token, GOMEMLIMIT, ...) win: ApplyEnv skips
	// an entry whose name the container already carries.
	Env []corev1.EnvVar
	// set reports which parts the environment provided.
	hasResources, hasProbes bool
}

// Scheduling is the Pod placement part of the defaults.
type Scheduling struct {
	PriorityClassName             string                            `json:"priorityClassName,omitempty"`
	NodeSelector                  map[string]string                 `json:"nodeSelector,omitempty"`
	Tolerations                   []corev1.Toleration               `json:"tolerations,omitempty"`
	Affinity                      *corev1.Affinity                  `json:"affinity,omitempty"`
	TopologySpreadConstraints     []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	TerminationGracePeriodSeconds *int64                            `json:"terminationGracePeriodSeconds,omitempty"`
}

// ProbeConfig is one probe's settings; Enabled false omits the probe.
type ProbeConfig struct {
	Enabled             bool  `json:"enabled"`
	PeriodSeconds       int32 `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int32 `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int32 `json:"failureThreshold,omitempty"`
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
}

// Probes are the three container probes, all HTTP GET /healthz on the
// container's health port.
type Probes struct {
	Startup   ProbeConfig `json:"startup"`
	Readiness ProbeConfig `json:"readiness"`
	Liveness  ProbeConfig `json:"liveness"`
}

// LoadPodDefaults reads GOFI_<prefix>_RESOURCES, GOFI_<prefix>_SCHEDULING,
// and GOFI_<prefix>_PROBES (prefix "LISTENER" or "WORKER"). Missing or empty
// variables leave the corresponding part unset; malformed JSON is an error.
func LoadPodDefaults(prefix string) (PodDefaults, error) {
	var out PodDefaults
	if raw := strings.TrimSpace(os.Getenv("GOFI_" + prefix + "_RESOURCES")); raw != "" && raw != "null" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &out.Resources); err != nil {
			return out, fmt.Errorf("GOFI_%s_RESOURCES: %w", prefix, err)
		}
		out.hasResources = true
	}
	if raw := strings.TrimSpace(os.Getenv("GOFI_" + prefix + "_SCHEDULING")); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &out.Scheduling); err != nil {
			return out, fmt.Errorf("GOFI_%s_SCHEDULING: %w", prefix, err)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("GOFI_" + prefix + "_PROBES")); raw != "" && raw != "null" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &out.Probes); err != nil {
			return out, fmt.Errorf("GOFI_%s_PROBES: %w", prefix, err)
		}
		out.hasProbes = true
	}
	if raw := strings.TrimSpace(os.Getenv("GOFI_" + prefix + "_ENV")); raw != "" && raw != "null" && raw != "[]" {
		if err := json.Unmarshal([]byte(raw), &out.Env); err != nil {
			return out, fmt.Errorf("GOFI_%s_ENV: %w", prefix, err)
		}
		seen := map[string]bool{}
		for _, e := range out.Env {
			if e.Name == "" {
				return out, fmt.Errorf("GOFI_%s_ENV: every entry needs a name", prefix)
			}
			if seen[e.Name] {
				return out, fmt.Errorf("GOFI_%s_ENV: %s is listed twice", prefix, e.Name)
			}
			seen[e.Name] = true
			if prefix == "WORKER" && reservedForWorkers(e.Name) {
				return out, fmt.Errorf("GOFI_%s_ENV: %s would hand every Python library a model credential or send its data to a tracing service; configure AI under Administration instead", prefix, e.Name)
			}
		}
	}
	return out, nil
}

// reservedForWorkers names environment variables the LLM SDKs installed in
// the worker image read on their own (credentials, endpoints, telemetry).
// Workers run user libraries, which can read os.environ, so an operator
// must not set them cluster-wide; GoFi provides model access through the
// assistant configuration instead.
func reservedForWorkers(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"OPENAI_", "ANTHROPIC_", "LANGSMITH_", "LANGCHAIN_", "GROQ_", "OPENROUTER_", "MISTRAL_", "COHERE_", "HUGGINGFACE", "HF_TOKEN", "GOFI_LLM_"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// ApplyEnv appends the configured extra environment to container, skipping
// names the container already sets (driver-owned variables keep their value).
func (d PodDefaults) ApplyEnv(container *corev1.Container) {
	if len(d.Env) == 0 {
		return
	}
	owned := make(map[string]bool, len(container.Env))
	for _, e := range container.Env {
		owned[e.Name] = true
	}
	for _, e := range d.Env {
		if owned[e.Name] {
			continue
		}
		container.Env = append(container.Env, *e.DeepCopy())
	}
}

// HasResources reports whether the environment supplied resources.
func (d PodDefaults) HasResources() bool { return d.hasResources }

// ResourcesOr returns the configured resources, or fallback when none were.
func (d PodDefaults) ResourcesOr(fallback corev1.ResourceRequirements) corev1.ResourceRequirements {
	if d.hasResources {
		return *d.Resources.DeepCopy()
	}
	return fallback
}

// ApplyScheduling sets the placement fields on a Pod spec. An unset
// terminationGracePeriodSeconds keeps the value already in the spec.
func (d PodDefaults) ApplyScheduling(spec *corev1.PodSpec) {
	s := d.Scheduling
	if s.PriorityClassName != "" {
		spec.PriorityClassName = s.PriorityClassName
	}
	if len(s.NodeSelector) > 0 {
		spec.NodeSelector = s.NodeSelector
	}
	if len(s.Tolerations) > 0 {
		spec.Tolerations = append([]corev1.Toleration(nil), s.Tolerations...)
	}
	if s.Affinity != nil {
		spec.Affinity = s.Affinity.DeepCopy()
	}
	if len(s.TopologySpreadConstraints) > 0 {
		spec.TopologySpreadConstraints = append([]corev1.TopologySpreadConstraint(nil), s.TopologySpreadConstraints...)
	}
	if s.TerminationGracePeriodSeconds != nil {
		grace := *s.TerminationGracePeriodSeconds
		spec.TerminationGracePeriodSeconds = &grace
	}
}

// ApplyProbes sets the container's probes (HTTP GET path on port) from the
// configured defaults; when none were configured the container is left as is.
func (d PodDefaults) ApplyProbes(container *corev1.Container, port int, path string) {
	if !d.hasProbes {
		return
	}
	container.StartupProbe = d.Probes.Startup.probe(port, path)
	container.ReadinessProbe = d.Probes.Readiness.probe(port, path)
	container.LivenessProbe = d.Probes.Liveness.probe(port, path)
}

func (p ProbeConfig) probe(port int, path string) *corev1.Probe {
	if !p.Enabled {
		return nil
	}
	return &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt(port)}},
		PeriodSeconds:       p.PeriodSeconds,
		TimeoutSeconds:      p.TimeoutSeconds,
		FailureThreshold:    p.FailureThreshold,
		InitialDelaySeconds: p.InitialDelaySeconds,
	}
}

// GoMemLimit is the GOMEMLIMIT for a container with the given limits: 90% of
// the memory limit in bytes, so the Go runtime collects before the kernel
// kills the Pod. It is "" when no memory limit is set.
func GoMemLimit(limits corev1.ResourceList) string {
	memory, ok := limits[corev1.ResourceMemory]
	if !ok || memory.IsZero() {
		return ""
	}
	return strconv.FormatInt(memory.Value()/10*9, 10)
}

// EnvBool reads a boolean environment switch with a fallback.
func EnvBool(name string, fallback bool) bool {
	if raw := os.Getenv(name); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			return v
		}
	}
	return fallback
}
