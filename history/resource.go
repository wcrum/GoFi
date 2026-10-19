package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/wcrum/gofi/spec"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Every backend stores a revision as the same FlowRevision resource, so a
// file, a Kubernetes object, and (later) a file in a Git repository are
// interchangeable and readable with the same tooling.
const (
	APIVersion = "gofi.wcrum.dev/v1alpha1"
	Kind       = "FlowRevision"
	// FlowLabel selects a flow's revisions (see flowLabel for long names).
	FlowLabel = "gofi.wcrum.dev/flow"
	// RevisionLabel carries the revision number.
	RevisionLabel = "gofi.wcrum.dev/revision"
)

type resource struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   resourceMeta `json:"metadata"`
	Spec       resourceSpec `json:"spec"`
}

type resourceMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type resourceSpec struct {
	Flow       string          `json:"flow"`
	Revision   int64           `json:"revision"`
	CreatedAt  string          `json:"createdAt"`
	Author     string          `json:"author,omitempty"`
	Message    string          `json:"message,omitempty"`
	Source     Source          `json:"source"`
	Hash       string          `json:"hash,omitempty"`
	Deleted    bool            `json:"deleted,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

// validFlowName guards paths and object names: only names a Flow resource
// can have are recorded.
func validFlowName(name string) error {
	if errs := validation.IsDNS1123Subdomain(name); len(errs) != 0 {
		return fmt.Errorf("flow name %q is not a valid resource name", name)
	}
	return nil
}

// objectName names a revision object: <flow>.r<number>, which is a valid
// DNS subdomain and maps back to exactly one (flow, number). A flow name too
// long for the suffix is replaced by a digest of it.
func objectName(flow string, number int64) string {
	suffix := ".r" + strconv.FormatInt(number, 10)
	if len(flow)+len(suffix) > validation.DNS1123SubdomainMaxLength {
		return "h-" + digest(flow) + suffix
	}
	return flow + suffix
}

// flowLabel is the flow's name when it is a valid label value, otherwise a
// digest of it; spec.flow always holds the full name.
func flowLabel(flow string) string {
	if len(validation.IsValidLabelValue(flow)) == 0 {
		return flow
	}
	return "h-" + digest(flow)
}

func digest(value string) string {
	sum := Hash(spec.FlowDefinition{Name: value})
	return sum[:40]
}

func toResource(rev Revision) (resource, error) {
	out := resource{
		APIVersion: APIVersion, Kind: Kind,
		Metadata: resourceMeta{Name: objectName(rev.Flow, rev.Number), Labels: map[string]string{
			FlowLabel: flowLabel(rev.Flow), RevisionLabel: strconv.FormatInt(rev.Number, 10),
			"app.kubernetes.io/managed-by": "gofi",
		}},
		Spec: resourceSpec{
			Flow: rev.Flow, Revision: rev.Number, CreatedAt: rev.CreatedAt.UTC().Format(time.RFC3339Nano),
			Author: rev.Author, Message: rev.Message, Source: rev.Source, Hash: rev.Hash, Deleted: rev.Deleted,
		},
	}
	if rev.Definition != nil {
		def := *rev.Definition
		def.Name = "" // the resource's spec.flow owns the name
		data, err := json.Marshal(def)
		if err != nil {
			return out, err
		}
		out.Spec.Definition = data
	}
	return out, nil
}

func fromResource(item resource) (Revision, error) {
	if item.APIVersion != APIVersion || item.Kind != Kind {
		return Revision{}, fmt.Errorf("%q is not a %s", item.Metadata.Name, Kind)
	}
	s := item.Spec
	if s.Flow == "" || s.Revision <= 0 {
		return Revision{}, fmt.Errorf("%s %q has no flow or revision", Kind, item.Metadata.Name)
	}
	created, err := time.Parse(time.RFC3339Nano, s.CreatedAt)
	if err != nil {
		return Revision{}, fmt.Errorf("%s %q: createdAt: %w", Kind, item.Metadata.Name, err)
	}
	rev := Revision{Flow: s.Flow, Number: s.Revision, CreatedAt: created, Author: s.Author, Message: s.Message, Source: s.Source, Hash: s.Hash, Deleted: s.Deleted}
	if len(bytes.TrimSpace(s.Definition)) > 0 && string(bytes.TrimSpace(s.Definition)) != "null" {
		var def spec.FlowDefinition
		if err := json.Unmarshal(s.Definition, &def); err != nil {
			return Revision{}, fmt.Errorf("%s %q: definition: %w", Kind, item.Metadata.Name, err)
		}
		def.Name = s.Flow
		rev.Definition = &def
	}
	return rev, nil
}
