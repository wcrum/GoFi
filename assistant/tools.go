package assistant

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
)

// Actions are the deployment operations the tools call. The api package
// implements them on top of the same handlers the portal uses, so every
// validation, redaction, and history rule applies to the assistant too.
// Results are text for the model (YAML or short sentences).
type Actions interface {
	ListFlows(ctx context.Context) (string, error)
	GetFlow(ctx context.Context, name string) (string, error)
	// FlowProblems is what the flow's Problems tab shows plus a check of
	// every Python step's library and function binding.
	FlowProblems(ctx context.Context, name string) (string, error)
	// ValidateFlow runs every check a save runs and returns warnings.
	ValidateFlow(ctx context.Context, flowYAML string) (string, error)
	ApplyFlow(ctx context.Context, flowYAML, message string, meta CallMeta) (string, error)

	CheckExpression(ctx context.Context, expression, resultType string) (string, error)
	ListExpressions(ctx context.Context) (string, error)
	ApplyExpression(ctx context.Context, name, expression, description, resultType string, meta CallMeta) (string, error)

	ListLibraries(ctx context.Context) (string, error)
	GetLibrary(ctx context.Context, name string) (string, error)
	ValidatePythonSource(ctx context.Context, source string) (string, error)
	ApplyLibrary(ctx context.Context, name, source string, timeoutSeconds, memoryMiB int, meta CallMeta) (string, error)

	ListEndpoints(ctx context.Context) (string, error)
	DescribeDeployment(ctx context.Context) (string, error)
}

// CallMeta attributes a change to the person chatting: the request headers
// carry the identity the history recorder reads (history.AuthorFromHeader).
type CallMeta struct {
	Author string
	Header http.Header
}

// Emitter receives side effects a tool asks the portal to perform.
type Emitter interface {
	Navigate(href string)
	// Highlight shows one block of a flow next to the reply.
	Highlight(flow, block, note string)
}

// flowBlocks are the pipeline positions point_at_flow_block accepts.
var flowBlocks = []string{"source", "frame", "decode", "parse", "encode"}

func validBlock(block string) bool {
	for _, b := range flowBlocks {
		if block == b {
			return true
		}
	}
	return strings.HasPrefix(block, "stage:") || strings.HasPrefix(block, "sink:")
}

// Tool is one function the model may call.
type Tool struct {
	Name        string
	Description string
	// Mutating tools are offered only when the configuration allows changes.
	Mutating bool
	Schema   anthropic.ToolInputSchemaParam
	Run      func(ctx context.Context, input json.RawMessage, meta CallMeta, emit Emitter) (string, error)
}

func (t Tool) param() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{Name: t.Name, Description: anthropic.String(t.Description), InputSchema: t.Schema}}
}

func schema(required []string, properties map[string]any) anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{Properties: properties, Required: required, ExtraFields: map[string]any{"additionalProperties": false}}
}

func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

// MaxToolOutput bounds what one tool result feeds back to the model.
const MaxToolOutput = 48 << 10

func clip(s string) string {
	if len(s) > MaxToolOutput {
		return s[:MaxToolOutput] + "\n[truncated]"
	}
	return s
}

// Tools lists every tool for actions and docs; enabled tools are those the
// model is offered under allowChanges.
func Tools(actions Actions, docs fs.FS) []Tool {
	decode := func(input json.RawMessage, into any) error {
		if len(input) == 0 {
			return nil
		}
		return json.Unmarshal(input, into)
	}
	tools := []Tool{
		{Name: "list_flows", Description: "List this deployment's flows (name, source, stages, sinks, runtime status). Call this before answering questions about what exists.",
			Schema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return actions.ListFlows(ctx)
			}},
		{Name: "get_flow", Description: "The full definition of one flow as YAML (credentials redacted as ***). Use it before proposing an edit so the edit keeps every other field.",
			Schema: schema([]string{"name"}, map[string]any{"name": prop("string", "The flow name")}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct{ Name string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.GetFlow(ctx, in.Name)
			}},
		{Name: "get_flow_problems", Description: "The open problems of one flow as the portal's Problems tab shows them (component, severity, message, how often, last seen), plus a binding check of every Python step: whether the referenced library exists, whether its worker is ready, and whether the function is registered. Call this for any flow whose status or runtime looks wrong before explaining the cause.",
			Schema: schema([]string{"flow"}, map[string]any{"flow": prop("string", "The flow name")}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct{ Flow string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.FlowProblems(ctx, in.Flow)
			}},
		{Name: "validate_flow", Description: "Run every check a save runs on a complete flow definition (YAML, the shape get_flow returns or docs/flows.md describes) without saving. Returns 'valid' with any warnings, or the validation error to fix. Always validate before apply_flow, and show the user a validated definition when you cannot apply it.",
			Schema: schema([]string{"flow_yaml"}, map[string]any{"flow_yaml": prop("string", "The complete flow definition as YAML")}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct {
					FlowYAML string `json:"flow_yaml"`
				}
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.ValidateFlow(ctx, in.FlowYAML)
			}},
		{Name: "apply_flow", Mutating: true, Description: "Create or replace a flow from a complete YAML definition. Validation, inline-Python checks, admission, and revision history apply exactly as for a portal save. Tell the user what changed and link the flow afterwards.",
			Schema: schema([]string{"flow_yaml"}, map[string]any{"flow_yaml": prop("string", "The complete flow definition as YAML"), "message": prop("string", "A short note recorded on the flow's new revision")}),
			Run: func(ctx context.Context, input json.RawMessage, meta CallMeta, _ Emitter) (string, error) {
				var in struct {
					FlowYAML string `json:"flow_yaml"`
					Message  string `json:"message"`
				}
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.ApplyFlow(ctx, in.FlowYAML, in.Message, meta)
			}},
		{Name: "check_cel", Description: "Compile a CEL expression against GoFi's message environment (fields, meta, raw, now, ...) and report its type or the diagnostics with line and column. result_type is 'bool' for filters and sink conditions or 'any' for transform values and dedup keys.",
			Schema: schema([]string{"expression"}, map[string]any{"expression": prop("string", "The CEL expression"), "result_type": map[string]any{"type": "string", "enum": []string{"bool", "any"}, "description": "Expected result type; default bool"}}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct {
					Expression string `json:"expression"`
					ResultType string `json:"result_type"`
				}
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.CheckExpression(ctx, in.Expression, in.ResultType)
			}},
		{Name: "list_expressions", Description: "List the saved CEL library: each expression's name, description, text, result type, and generation.",
			Schema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return actions.ListExpressions(ctx)
			}},
		{Name: "apply_expression", Mutating: true, Description: "Save a CEL expression to the library (new or replacing by name). Flows that reference the latest generation are recompiled. Check it with check_cel first.",
			Schema: schema([]string{"name", "expression"}, map[string]any{"name": prop("string", "1-63 lowercase letters, digits, or hyphens, starting with a letter"), "expression": prop("string", "The CEL expression"), "description": prop("string", "What it is for"), "result_type": map[string]any{"type": "string", "enum": []string{"bool", "any"}, "description": "bool for filters and conditions, any for values"}}),
			Run: func(ctx context.Context, input json.RawMessage, meta CallMeta, _ Emitter) (string, error) {
				var in struct {
					Name        string `json:"name"`
					Expression  string `json:"expression"`
					Description string `json:"description"`
					ResultType  string `json:"result_type"`
				}
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.ApplyExpression(ctx, in.Name, in.Expression, in.Description, in.ResultType, meta)
			}},
		{Name: "list_libraries", Description: "List the Python function libraries (sandboxes): name, worker status, registered functions by kind, and egress rules.",
			Schema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return actions.ListLibraries(ctx)
			}},
		{Name: "get_library", Description: "One library's full source and settings.",
			Schema: schema([]string{"name"}, map[string]any{"name": prop("string", "The library name")}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct{ Name string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.GetLibrary(ctx, in.Name)
			}},
		{Name: "validate_python_source", Description: "Statically check Python library source (syntax and undefined names) without executing it. Decorators: @gofi.framer, decoder, parser, transformer, encoder, sink, fetcher, streamer; read docs/python.md for their signatures. Always validate before apply_library.",
			Schema: schema([]string{"source"}, map[string]any{"source": prop("string", "The complete library source")}),
			Run: func(ctx context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct{ Source string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.ValidatePythonSource(ctx, in.Source)
			}},
		{Name: "apply_library", Mutating: true, Description: "Create or replace a Python function library and (re)start its isolated worker. Functions register once the worker is ready; the worker has no network unless the library's egress rules allow it.",
			Schema: schema([]string{"name", "source"}, map[string]any{"name": prop("string", "The library name (lowercase, hyphens)"), "source": prop("string", "The complete library source"), "timeout_seconds": prop("integer", "Per-call deadline, 1-30; default 5"), "memory_mib": prop("integer", "Worker memory limit; default 128")}),
			Run: func(ctx context.Context, input json.RawMessage, meta CallMeta, _ Emitter) (string, error) {
				var in struct {
					Name           string `json:"name"`
					Source         string `json:"source"`
					TimeoutSeconds int    `json:"timeout_seconds"`
					MemoryMiB      int    `json:"memory_mib"`
				}
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return actions.ApplyLibrary(ctx, in.Name, in.Source, in.TimeoutSeconds, in.MemoryMiB, meta)
			}},
		{Name: "list_endpoints", Description: "Destinations GoFi can discover for sinks: containers, Services, and hosts with their URLs.",
			Schema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return actions.ListEndpoints(ctx)
			}},
		{Name: "describe_deployment", Description: "How this GoFi runs: deployment mode, runtime driver (docker, kubernetes, in-process), and what that allows.",
			Schema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return actions.DescribeDeployment(ctx)
			}},
		{Name: "list_docs", Description: "The GoFi documentation pages available to read_docs, with their titles.",
			Schema: schema(nil, map[string]any{}),
			Run: func(_ context.Context, _ json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				return listDocs(docs)
			}},
		{Name: "read_docs", Description: readDocsDescription(docs),
			Schema: schema([]string{"name"}, map[string]any{
				"name":    prop("string", "The page name, without .md (a close name such as 'functions' or 'flow' resolves to the page it means)"),
				"section": prop("string", "A heading of the page (or a word from it) to read just that section; a long page without a section returns its table of contents plus the opening section"),
			}),
			Run: func(_ context.Context, input json.RawMessage, _ CallMeta, _ Emitter) (string, error) {
				var in struct{ Name, Section string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				return readDocSection(docs, in.Name, in.Section)
			}},
		{Name: "point_at_flow_block", Description: "Show the user which part of a flow you are talking about: the portal draws the flow's pipeline (source, frame, decode, parse, stages, encode, sinks) next to your reply with that block highlighted and your note on it. Use it whenever you explain a problem or a change in a specific step; call it once per block, then keep explaining in text.",
			Schema: schema([]string{"flow", "block"}, map[string]any{"flow": prop("string", "The flow name"), "block": prop("string", "source, frame, decode, parse, encode, stage:<stage id>, or sink:<sink id or index>"), "note": prop("string", "One sentence shown on the highlighted block")}),
			Run: func(_ context.Context, input json.RawMessage, _ CallMeta, emit Emitter) (string, error) {
				var in struct{ Flow, Block, Note string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				in.Block = strings.TrimSpace(in.Block)
				if strings.TrimSpace(in.Flow) == "" || !validBlock(in.Block) {
					return "", fmt.Errorf("flow and a block (source, frame, decode, parse, encode, stage:<id>, sink:<id>) are required")
				}
				if len(in.Note) > 300 {
					in.Note = in.Note[:300]
				}
				emit.Highlight(in.Flow, in.Block, in.Note)
				return "highlighted " + in.Block + " of " + in.Flow + " for the user", nil
			}},
		{Name: "open_page", Description: "Send the user's browser to a GoFi page: home, flows, new-flow, flow, flow-edit, flow-traffic, flow-problems, explore, functions, cel, endpoints, cluster, administration. 'name' is the flow, library, or expression for pages that take one. Use it when the user asks to go somewhere or after creating something they should look at.",
			Schema: schema([]string{"page"}, map[string]any{"page": prop("string", "The page id"), "name": prop("string", "The flow, library, or expression name")}),
			Run: func(_ context.Context, input json.RawMessage, _ CallMeta, emit Emitter) (string, error) {
				var in struct{ Page, Name string }
				if err := decode(input, &in); err != nil {
					return "", err
				}
				href, err := pageHref(in.Page, in.Name)
				if err != nil {
					return "", err
				}
				emit.Navigate(href)
				return "opened " + href, nil
			}},
	}
	return tools
}

// pageHref mirrors ui/src/router.ts paths.
func pageHref(page, name string) (string, error) {
	enc := url.PathEscape(name)
	query := url.QueryEscape(name)
	switch page {
	case "home":
		return "#/", nil
	case "flows":
		return "#/flows", nil
	case "new-flow":
		return "#/flows/new", nil
	case "flow", "flow-edit", "flow-traffic", "flow-problems":
		if name == "" {
			return "", fmt.Errorf("page %s needs a flow name", page)
		}
		if page == "flow" {
			return "#/flows/" + enc, nil
		}
		return "#/flows/" + enc + "/" + strings.TrimPrefix(page, "flow-"), nil
	case "explore":
		if name == "" {
			return "#/explore", nil
		}
		return "#/explore?flow=" + query, nil
	case "functions", "libraries":
		if name == "" {
			return "#/functions", nil
		}
		return "#/functions?name=" + query, nil
	case "cel", "expressions":
		if name == "" {
			return "#/cel", nil
		}
		return "#/cel?name=" + query, nil
	case "endpoints":
		return "#/connections/endpoints", nil
	case "cluster":
		return "#/cluster", nil
	case "administration", "admin":
		return "#/administration", nil
	}
	return "", fmt.Errorf("unknown page %q", page)
}

func listDocs(docs fs.FS) (string, error) {
	if docs == nil {
		return "no documentation is embedded in this build", nil
	}
	names := docNames(docs)
	var b strings.Builder
	for _, name := range names {
		title := ""
		if f, err := docs.Open(name + ".md"); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				if line := strings.TrimSpace(scanner.Text()); strings.HasPrefix(line, "# ") {
					title = strings.TrimPrefix(line, "# ")
					break
				}
			}
			f.Close()
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, title)
	}
	return b.String(), nil
}

// docNames lists the embedded documentation pages, sorted, without .md.
func docNames(docs fs.FS) []string {
	if docs == nil {
		return nil
	}
	entries, err := fs.ReadDir(docs, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)
	return names
}

// readDocsDescription names the pages up front so a model need not call
// list_docs first (small models often guess a page name instead).
func readDocsDescription(docs fs.FS) string {
	d := "Read one documentation page by name, or one section of it with 'section'. Prefer this over memory for GoFi's YAML shapes, Python decorators, and the CEL environment. Long pages come back as a table of contents: pick a section next, since whole pages do not fit the model's request budget."
	if names := docNames(docs); len(names) > 0 {
		d += " Pages: " + strings.Join(names, ", ") + ". Flows, sources, sinks, stages, and CEL are under 'flows'; Python libraries, functions, and decorators under 'python'."
	}
	return d
}

// docAliases maps the names models reach for to the page that covers them.
var docAliases = map[string]string{
	"flow": "flows", "pipeline": "flows", "pipelines": "flows", "source": "flows", "sources": "flows", "sink": "flows", "sinks": "flows",
	"stage": "flows", "stages": "flows", "cel": "flows", "expression": "flows", "expressions": "flows", "filter": "flows", "filters": "flows", "dedup": "flows",
	"function": "python", "functions": "python", "library": "python", "libraries": "python", "decorator": "python", "decorators": "python",
	"python-functions": "python", "python-library": "python", "sandbox": "python", "sandboxes": "python", "worker": "python", "workers": "python",
	"config": "configuration", "settings": "configuration", "deploy": "deployment", "kubernetes": "deployment", "helm": "deployment", "docker": "deployment",
	"metrics": "observability", "tracing": "observability", "logging": "observability", "logs": "observability", "assistant": "assistant", "gofibot": "assistant",
}

// resolveDoc finds the page a name means: the exact page, a case- and
// separator-insensitive match, a known alias, or the one page whose name
// starts with or contains it. "" when nothing fits.
func resolveDoc(names []string, name string) string {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.TrimSuffix(s, ".md")
		s = strings.NewReplacer(" ", "-", "_", "-").Replace(s)
		return s
	}
	want := norm(name)
	if want == "" {
		return ""
	}
	has := func(n string) bool {
		for _, candidate := range names {
			if candidate == n {
				return true
			}
		}
		return false
	}
	if has(want) {
		return want
	}
	for _, candidate := range names {
		if norm(candidate) == want {
			return candidate
		}
	}
	if alias, ok := docAliases[want]; ok && has(alias) {
		return alias
	}
	if alias, ok := docAliases[strings.TrimSuffix(want, "s")]; ok && has(alias) {
		return alias
	}
	var matches []string
	for _, candidate := range names {
		if strings.HasPrefix(norm(candidate), want) || strings.HasPrefix(want, norm(candidate)) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		for _, candidate := range names {
			if strings.Contains(norm(candidate), want) || strings.Contains(want, norm(candidate)) {
				matches = append(matches, candidate)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// MaxDocSection bounds what one read_docs call returns. Whole pages run to
// 30 KB and more, which exceeds the per-request token allowance of small
// hosted models (a Groq free tier answers HTTP 413), so a long page is
// served as its table of contents and sections are read one at a time.
const MaxDocSection = 10 << 10

// docSection is one heading of a page with the text under it (up to the
// next heading of the same or a higher level).
type docSection struct {
	Level int
	Title string
	Body  string // the heading line included
}

// splitDocSections cuts a Markdown page at its ATX headings; fenced code is
// never cut. The text before the first heading is a section with level 0.
func splitDocSections(page string) []docSection {
	var sections []docSection
	var current *docSection
	var body strings.Builder
	inFence := false
	flush := func() {
		if current != nil {
			current.Body = strings.TrimRight(body.String(), "\n") + "\n"
			sections = append(sections, *current)
		}
		body.Reset()
	}
	for _, line := range strings.Split(page, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if !inFence {
			if m := docHeading.FindStringSubmatch(line); m != nil {
				flush()
				current = &docSection{Level: len(m[1]), Title: strings.TrimSpace(m[2])}
			} else if current == nil {
				current = &docSection{Level: 0, Title: ""}
			}
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return sections
}

var docHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// findDocSection picks the section whose title best matches want: an exact
// title (case-insensitive), then a title containing every word of want,
// then the first title containing want. The matched section's body runs
// until the next heading of the same or a higher level.
func findDocSection(sections []docSection, want string) (string, bool) {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(strings.TrimLeft(s, "# "))) }
	want = norm(want)
	if want == "" {
		return "", false
	}
	pick := -1
	for i, sec := range sections {
		if norm(sec.Title) == want {
			pick = i
			break
		}
	}
	if pick < 0 {
		words := strings.Fields(want)
		for i, sec := range sections {
			title := norm(sec.Title)
			all := true
			for _, w := range words {
				if !strings.Contains(title, w) {
					all = false
					break
				}
			}
			if all && title != "" {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		for i, sec := range sections {
			if strings.Contains(norm(sec.Title), want) {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(sections[pick].Body)
	for _, sec := range sections[pick+1:] {
		if sec.Level <= sections[pick].Level {
			break
		}
		b.WriteString(sec.Body)
	}
	return b.String(), true
}

// docOutline is a page's table of contents: every heading with its size,
// so the model can ask for the section it needs.
func docOutline(name string, sections []docSection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sections of %s (read one with read_docs name=%q section=<title>):\n", name, name)
	for _, sec := range sections {
		if sec.Title == "" {
			continue
		}
		fmt.Fprintf(&b, "%s- %s (%d chars)\n", strings.Repeat("  ", max(0, sec.Level-1)), sec.Title, len(sec.Body))
	}
	return b.String()
}

// clipSection bounds one section's text, saying how to read the rest.
func clipSection(text string) string {
	if len(text) <= MaxDocSection {
		return text
	}
	cut := strings.LastIndex(text[:MaxDocSection], "\n")
	if cut < MaxDocSection/2 {
		cut = MaxDocSection
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	return text[:cut] + "\n[truncated: this section is long; ask for one of its sub-headings with 'section']\n"
}

// readDocSection serves a page in pieces the model can take: a named
// section when asked (its sub-sections included); otherwise the whole page
// when it is short, or its table of contents and opening section when not.
func readDocSection(docs fs.FS, name, section string) (string, error) {
	page, err := readDoc(docs, name)
	if err != nil {
		return "", err
	}
	sections := splitDocSections(page)
	if section = strings.TrimSpace(section); section != "" {
		text, ok := findDocSection(sections, section)
		if !ok {
			return "", fmt.Errorf("page %q has no section matching %q\n%s", name, section, docOutline(name, sections))
		}
		return clipSection(text), nil
	}
	if len(page) <= MaxDocSection {
		return page, nil
	}
	var b strings.Builder
	b.WriteString(docOutline(name, sections))
	b.WriteString("\n")
	if len(sections) > 0 {
		opening := sections[0].Body
		if len(sections) > 1 && sections[0].Level == 0 {
			opening += sections[1].Body
		}
		b.WriteString(clipSection(opening))
	}
	return b.String(), nil
}

func readDoc(docs fs.FS, name string) (string, error) {
	if docs == nil {
		return "", fmt.Errorf("no documentation is embedded in this build")
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("unknown documentation page %q", name)
	}
	names := docNames(docs)
	page := resolveDoc(names, name)
	if page == "" {
		return "", fmt.Errorf("unknown documentation page %q; the pages are: %s", name, strings.Join(names, ", "))
	}
	data, err := fs.ReadFile(docs, page+".md")
	if err != nil {
		return "", fmt.Errorf("unknown documentation page %q; the pages are: %s", name, strings.Join(names, ", "))
	}
	return string(data), nil
}
