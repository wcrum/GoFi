package expressions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wcrum/gofi/spec"
)

func library() Library {
	return Library{
		"hot":  {Name: "hot", Expression: `fields["t"] > 30.0`, ResultType: "bool", Generation: 3, Revisions: []Revision{{Generation: 2, Expression: `fields["t"] > 20.0`}}},
		"key":  {Name: "key", Expression: `fields["id"]`, ResultType: "any", Generation: 1},
		"warm": {Name: "warm", Expression: `fields["t"] > 10.0`, ResultType: "bool", Generation: 1},
	}
}

func TestResolveLinksStagesAndSinksWithoutMutatingInput(t *testing.T) {
	def := spec.FlowDefinition{
		Name: "l",
		Stages: []spec.StageSpec{
			{ID: "follow", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "hot"}},
			{ID: "pinned", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "hot", Generation: 2}},
			{ID: "literal", Kind: "filter", Type: "cel", Expression: "true", ExpressionRef: &spec.ExpressionRef{Name: "hot"}},
			{ID: "dedup", Kind: "dedup", ExpressionRef: &spec.ExpressionRef{Name: "key"}},
		},
		Sinks:          []spec.SinkCfg{{Type: "stdout", WhenRef: &spec.ExpressionRef{Name: "warm"}}},
		DeadLetterSink: &spec.SinkCfg{Type: "stdout", WhenRef: &spec.ExpressionRef{Name: "warm"}},
	}
	out, err := Resolve(def, library())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`fields["t"] > 30.0`, `fields["t"] > 20.0`, "true"}
	for i, w := range want {
		if out.Stages[i].Expression != w {
			t.Fatalf("stage %d: got %q want %q", i, out.Stages[i].Expression, w)
		}
	}
	if out.Stages[3].KeyExpr != `fields["id"]` || out.Sinks[0].When == "" || out.DeadLetterSink.When == "" {
		t.Fatalf("unresolved: %+v", out)
	}
	if def.Stages[0].Expression != "" || def.Sinks[0].When != "" || def.DeadLetterSink.When != "" {
		t.Fatal("Resolve mutated its input")
	}
}

func TestResolveRejectsMissingWrongTypeAndUnsupported(t *testing.T) {
	cases := map[string]spec.StageSpec{
		"does not exist":  {ID: "a", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "nope"}},
		"no generation 1": {ID: "a", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "hot", Generation: 1}},
		"requires a bool": {ID: "a", Kind: "filter", Type: "cel", ExpressionRef: &spec.ExpressionRef{Name: "key"}},
		"applies only to": {ID: "a", Kind: "filter", Type: "regex", ExpressionRef: &spec.ExpressionRef{Name: "hot"}},
	}
	for want, stage := range cases {
		_, err := Resolve(spec.FlowDefinition{Stages: []spec.StageSpec{stage}}, library())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestUsesDistinguishesFollowersFromPins(t *testing.T) {
	def := spec.FlowDefinition{Stages: []spec.StageSpec{{ID: "a", ExpressionRef: &spec.ExpressionRef{Name: "hot", Generation: 2}}}}
	if uses, follows := Uses(def, "hot", 3); !uses || follows {
		t.Fatalf("pinned: uses=%v follows=%v", uses, follows)
	}
	def.Stages[0].ExpressionRef.Generation = 0
	if uses, follows := Uses(def, "hot", 3); !uses || !follows {
		t.Fatalf("follow: uses=%v follows=%v", uses, follows)
	}
}

func TestValidateNormalizesAndCompiles(t *testing.T) {
	e, err := Validate(Expression{Name: "ok", Expression: "true", Tags: []string{" b", "a", "a", ""}})
	if err != nil || e.ResultType != "bool" || strings.Join(e.Tags, ",") != "a,b" {
		t.Fatalf("got %+v %v", e, err)
	}
	if _, err := Validate(Expression{Name: "bad", Expression: `"x"`}); err == nil {
		t.Fatal("expected bool result type to reject a string expression")
	}
	if _, err := Validate(Expression{Name: "Bad Name", Expression: "true"}); err == nil {
		t.Fatal("expected invalid name to be rejected")
	}
}

func TestFileStoreRoundTripAndManagerCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expressions.yaml")
	m := NewManager(&FileStore{Path: path})
	ctx := context.Background()
	if lib, err := m.Snapshot(ctx); err != nil || len(lib) != 0 {
		t.Fatalf("empty store: %v %v", lib, err)
	}
	e := Expression{Name: "hot", Expression: `fields["t"] > 30.0`, ResultType: "bool", Generation: 2, Tags: []string{"temp"}, Revisions: []Revision{{Generation: 1, Expression: "true"}}}
	if err := m.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "kind: CelExpression") || !strings.Contains(string(data), "name: hot") {
		t.Fatalf("unexpected file:\n%s", data)
	}
	lib, err := NewManager(&FileStore{Path: path}).Snapshot(ctx)
	if err != nil || lib["hot"].Generation != 2 || len(lib["hot"].Revisions) != 1 {
		t.Fatalf("reload: %+v %v", lib, err)
	}
	if err := m.Delete(ctx, "hot"); err != nil || len(m.Cached()) != 0 {
		t.Fatalf("delete: %v %v", m.Cached(), err)
	}
}
