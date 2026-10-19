package assistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNormalize(t *testing.T) {
	c, err := Normalize(Configuration{})
	if err != nil || c.MaxToolSteps != DefaultMaxToolSteps || c.ModelOrDefault() != DefaultModel {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, bad := range []Configuration{{BaseURL: "ftp://x"}, {BaseURL: "not a url"}, {BaseURL: "https://user:pw@host/"}, {MaxToolSteps: 99}, {Effort: "max"}, {Instructions: strings.Repeat("x", MaxInstructions+1)}} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("expected an error for %+v", bad)
		}
	}
	if c, err := Normalize(Configuration{BaseURL: " https://gateway.example/v1 ", Effort: "low"}); err != nil || c.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("trim: %+v %v", c, err)
	}
}

func TestFileStoreRoundTripKeepsKey(t *testing.T) {
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	t.Setenv("GOFI_ASSISTANT_BASE_URL", "")
	path := filepath.Join(t.TempDir(), "assistant.yaml")
	m := NewManager(&FileStore{Path: path})
	ctx := context.Background()
	if c, err := m.Get(ctx); err != nil || c.Enabled {
		t.Fatalf("empty store: %+v %v", c, err)
	}
	if _, err := m.Save(ctx, Configuration{Enabled: true, APIKey: "sk-secret", Model: "claude-opus-5-5"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	// A save without a key (the portal never sees it) keeps the stored one.
	saved, err := m.Save(ctx, Configuration{Enabled: true, Model: "claude-sonnet-5-5", AllowChanges: true})
	if err != nil || saved.APIKey != "sk-secret" || saved.Model != "claude-sonnet-5-5" {
		t.Fatalf("key not kept: %+v %v", saved, err)
	}
	if saved, err = m.Save(ctx, Configuration{Enabled: true, APIKey: "***"}); err != nil || saved.APIKey != "sk-secret" {
		t.Fatalf("sentinel not kept: %+v %v", saved, err)
	}
	if cleared, err := m.Save(ctx, Configuration{Enabled: true}, true); err != nil || cleared.APIKey != "" {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	if _, err := m.Save(ctx, Configuration{Enabled: true, APIKey: "sk-secret"}); err != nil {
		t.Fatal(err)
	}
	fresh := NewManager(&FileStore{Path: path})
	c, err := fresh.Get(ctx)
	if err != nil || c.APIKey != "sk-secret" || !c.Enabled {
		t.Fatalf("reload: %+v %v", c, err)
	}
	if key, source := Key(c); key != "sk-secret" || source != "stored" {
		t.Fatalf("key: %q %q", key, source)
	}
	if Public(c).APIKey != "" {
		t.Fatal("public configuration carries the key")
	}
}

func TestKeyFromEnvironment(t *testing.T) {
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	dir := t.TempDir()
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, []byte("sk-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", file)
	if key, source := Key(Configuration{}); key != "sk-file" || source != "environment" {
		t.Fatalf("file key: %q %q", key, source)
	}
	t.Setenv("GOFI_ASSISTANT_API_KEY", "sk-env")
	if key, _ := Key(Configuration{}); key != "sk-env" {
		t.Fatalf("env key: %q", key)
	}
	if key, _ := Key(Configuration{APIKey: "sk-stored"}); key != "sk-env" {
		t.Fatalf("environment key must win over a stored one: %q", key)
	}
	t.Setenv("GOFI_ASSISTANT_API_KEY", "")
	t.Setenv("GOFI_ASSISTANT_API_KEY_FILE", "")
	if key, source := Key(Configuration{APIKey: "sk-stored"}); key != "sk-stored" || source != "stored" {
		t.Fatalf("stored key: %q %q", key, source)
	}
}

func TestPageHref(t *testing.T) {
	cases := map[[2]string]string{
		{"home", ""}: "#/", {"flows", ""}: "#/flows", {"flow", "a b"}: "#/flows/a%20b", {"flow-edit", "x"}: "#/flows/x/edit",
		{"explore", "x"}: "#/explore?flow=x", {"cel", "n"}: "#/cel?name=n", {"functions", ""}: "#/functions", {"administration", ""}: "#/administration",
	}
	for in, want := range cases {
		if got, err := pageHref(in[0], in[1]); err != nil || got != want {
			t.Errorf("%v: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range [][2]string{{"flow", ""}, {"nope", ""}} {
		if _, err := pageHref(in[0], in[1]); err == nil {
			t.Errorf("%v: expected an error", in)
		}
	}
}

func TestDocsAreConfinedToEmbeddedPages(t *testing.T) {
	docs := fstest.MapFS{"flows.md": {Data: []byte("# Flows\nbody")}, "python.md": {Data: []byte("# Python\n")}}
	list, err := listDocs(docs)
	if err != nil || !strings.Contains(list, "- flows: Flows") || !strings.Contains(list, "- python: Python") {
		t.Fatalf("list: %q %v", list, err)
	}
	if body, err := readDoc(docs, "flows.md"); err != nil || body != "# Flows\nbody" {
		t.Fatalf("read: %q %v", body, err)
	}
	// Names a model reaches for resolve to the page that covers them.
	for in, want := range map[string]string{"Flow": "# Flows\nbody", "functions": "# Python\n", "Python Functions": "# Python\n", "CEL": "# Flows\nbody", "pyth": "# Python\n", "flows.md ": "# Flows\nbody"} {
		if body, err := readDoc(docs, in); err != nil || body != want {
			t.Errorf("%q: %q %v, want %q", in, body, err, want)
		}
	}
	for _, bad := range []string{"../go.mod", "security/x", ".hidden", "missing", ""} {
		if _, err := readDoc(docs, bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	if _, err := readDoc(docs, "missing"); err == nil || !strings.Contains(err.Error(), "flows, python") {
		t.Errorf("the error should name the pages: %v", err)
	}
	if d := readDocsDescription(docs); !strings.Contains(d, "Pages: flows, python") {
		t.Errorf("description should list pages: %q", d)
	}
}

func TestToolsHideMutatingWithoutAllowChanges(t *testing.T) {
	s := NewService(NewManager(&FileStore{Path: filepath.Join(t.TempDir(), "a.yaml")}), nil, nil)
	all := s.offered(false)
	for _, tool := range all {
		if tool.Mutating {
			t.Errorf("%s offered without allowChanges", tool.Name)
		}
	}
	if len(all) == 0 {
		t.Fatal("no tools offered")
	}
	allowed := s.offered(true)
	if len(allowed) <= len(all) {
		t.Fatal("allowChanges offers no extra tools")
	}
}

func TestTrimStartsAtAUserTextMessage(t *testing.T) {
	s := &Service{}
	c := &conversation{}
	for i := 0; i < maxMessages+10; i++ {
		c.messages = append(c.messages, message{Role: "tool", Results: []toolResult{{ID: "id", Output: "x"}}})
		c.messages = append(c.messages, message{Role: "user", Text: "hello"})
	}
	s.trim(c)
	if len(c.messages) > maxMessages || !c.messages[0].isUserText() {
		t.Fatalf("trimmed to %d, first text? %v", len(c.messages), c.messages[0].isUserText())
	}
}

func TestSystemPromptReflectsPermissions(t *testing.T) {
	ro := systemPrompt(Configuration{})
	rw := systemPrompt(Configuration{AllowChanges: true, Instructions: "Prefer NATS sinks."})
	if !strings.Contains(ro, "does not allow you to apply") || strings.Contains(ro, "apply_flow") {
		t.Fatalf("read-only prompt: %s", ro)
	}
	if !strings.Contains(rw, "apply_flow") || !strings.Contains(rw, "Prefer NATS sinks.") {
		t.Fatalf("read-write prompt: %s", rw)
	}
}
