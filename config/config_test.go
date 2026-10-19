package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != ProfileDefault {
		t.Fatalf("profile = %q", cfg.Profile)
	}
	if cfg.Limits.MaxFrameBytes != 1<<20 || cfg.Limits.SinkDropPolicy != DropBlock {
		t.Fatalf("unexpected defaults: %+v", cfg.Limits)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default preset must validate: %v", err)
	}
}

func TestPresetsValidateAndAreOrdered(t *testing.T) {
	edge, def, large := Preset(ProfileEdge), Preset(ProfileDefault), Preset(ProfileLarge)
	for _, c := range []Config{edge, def, large} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s preset invalid: %v", c.Profile, err)
		}
	}
	if !(edge.Limits.MaxFrameBytes < def.Limits.MaxFrameBytes && def.Limits.MaxFrameBytes < large.Limits.MaxFrameBytes) {
		t.Error("MaxFrameBytes not ordered edge < default < large")
	}
	if !(edge.Limits.SinkQueueDepth < def.Limits.SinkQueueDepth && def.Limits.SinkQueueDepth < large.Limits.SinkQueueDepth) {
		t.Error("SinkQueueDepth not ordered")
	}
	if !(edge.Limits.MaxConnectionsPerListener < def.Limits.MaxConnectionsPerListener && def.Limits.MaxConnectionsPerListener < large.Limits.MaxConnectionsPerListener) {
		t.Error("edge should allow fewer connections")
	}
	if edge.Limits.SinkDropPolicy != DropOldest {
		t.Error("edge should default to drop-oldest so a dead sink never stalls ingest")
	}
	if edge.Limits.HistoryLimit >= def.Limits.HistoryLimit {
		t.Error("edge history retention should be smaller to spare flash")
	}
	if edge.Memory.GOMEMLIMITFraction >= def.Memory.GOMEMLIMITFraction {
		t.Error("edge should leave more headroom for non-Go memory")
	}
}

func TestProfileSelection(t *testing.T) {
	for in, want := range map[string]Profile{"edge": ProfileEdge, "EDGE": ProfileEdge, "embedded": ProfileEdge, "": ProfileDefault, "large": ProfileLarge, "Server": ProfileLarge} {
		cfg, err := LoadFrom(env(map[string]string{"GOFI_PROFILE": in}))
		if err != nil || cfg.Profile != want {
			t.Errorf("GOFI_PROFILE=%q: profile=%q err=%v", in, cfg.Profile, err)
		}
	}
	if _, err := LoadFrom(env(map[string]string{"GOFI_PROFILE": "huge"})); err == nil || !strings.Contains(err.Error(), "GOFI_PROFILE") {
		t.Errorf("unknown profile must fail naming the variable, got %v", err)
	}
}

func TestOverridesAndParsing(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{
		"GOFI_PROFILE":                "edge",
		"GOFI_MAX_FRAME_BYTES":        "2Mi",
		"GOFI_FRAMER_INITIAL_BUFFER":  "8k",
		"GOFI_IDLE_TIMEOUT":           "90",
		"GOFI_SINK_RETRY_MAX_BACKOFF": "5m",
		"GOFI_SINK_DROP_POLICY":       "Drop-Newest",
		"GOFI_SINK_SPOOL_DIR":         "/var/lib/gofi/spool",
		"GOFI_SINK_SPOOL_MAX_BYTES":   "64MB",
		"GOFI_METRICS_ENABLED":        "off",
		"GOFI_LOG_LEVEL":              "debug",
		"GOFI_LOG_FORMAT":             "TEXT",
		"GOFI_MEMORY_LIMIT":           "128Mi",
		"GOFI_GOMEMLIMIT_FRACTION":    "0.5",
		"GOFI_GOMAXPROCS":             "2",
		"GOFI_TAP_RAW_TRUNCATE":       "0",
		"GOFI_SINK_BATCH_BYTES":       "512Ki",
		"GOFI_SINK_BREAKER_FAILURES":  "0",
		"GOFI_WATCH_QUEUE":            "16",
		"GOFI_HUB_IDLE_GRACE":         "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Limits
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"MaxFrameBytes", l.MaxFrameBytes, 2 << 20},
		{"FramerInitialBuffer", l.FramerInitialBuffer, 8 << 10},
		{"ConnIdleTimeout", l.ConnIdleTimeout, 90 * time.Second},
		{"SinkRetryMaxBackoff", l.SinkRetryMaxBackoff, 5 * time.Minute},
		{"SinkDropPolicy", l.SinkDropPolicy, DropNewest},
		{"SinkSpoolDir", l.SinkSpoolDir, "/var/lib/gofi/spool"},
		{"SinkSpoolMaxBytes", l.SinkSpoolMaxBytes, int64(64 << 20)},
		{"MetricsEnabled", cfg.Observability.MetricsEnabled, false},
		{"LogLevel", cfg.Observability.LogLevel, slog.LevelDebug},
		{"LogFormat", cfg.Observability.LogFormat, "text"},
		{"MemoryLimit", cfg.Memory.LimitBytes, int64(128 << 20)},
		{"Fraction", cfg.Memory.GOMEMLIMITFraction, 0.5},
		{"GOMAXPROCS", cfg.Memory.GOMAXPROCS, 2},
		{"TapRawTruncate", l.TapRawTruncate, 0},
		{"SinkBatchBytes", l.SinkBatchBytes, 512 << 10},
		{"SinkBreakerFailures", l.SinkBreakerFailures, 0},
		{"WatchQueue", cfg.Observability.WatchQueue, 16},
		{"HubIdleGrace", cfg.Observability.HubIdleGrace, time.Duration(0)},
		// Untouched edge values survive overrides.
		{"UDPWorkers(edge)", l.UDPWorkers, 1},
		{"SinkRetries(edge)", l.SinkRetries, 5},
		{"WatchMaxStreams(edge)", cfg.Observability.WatchMaxStreams, 32},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"frame too small":          {"GOFI_MAX_FRAME_BYTES": "10"},
		"initial > max":            {"GOFI_FRAMER_INITIAL_BUFFER": "2Mi"},
		"zero conns":               {"GOFI_MAX_CONNECTIONS": "0"},
		"bad policy":               {"GOFI_SINK_DROP_POLICY": "yolo"},
		"batch > depth":            {"GOFI_SINK_BATCH_MAX": "10000"},
		"backoff inverted":         {"GOFI_SINK_RETRY_MAX_BACKOFF": "10ms"},
		"spool too small":          {"GOFI_SINK_SPOOL_DIR": "/tmp/x", "GOFI_SINK_SPOOL_MAX_BYTES": "4k"},
		"not an int":               {"GOFI_UDP_WORKERS": "many"},
		"bad bytes":                {"GOFI_UDP_READ_BUFFER": "1.5Mi"},
		"bad duration":             {"GOFI_IDLE_TIMEOUT": "soon"},
		"bad bool":                 {"GOFI_TAP_ENABLED": "maybe"},
		"bad level":                {"GOFI_LOG_LEVEL": "loud"},
		"bad format":               {"GOFI_LOG_FORMAT": "xml"},
		"fraction > 1":             {"GOFI_GOMEMLIMIT_FRACTION": "1.5"},
		"fraction 0":               {"GOFI_GOMEMLIMIT_FRACTION": "0"},
		"memory limit tiny":        {"GOFI_MEMORY_LIMIT": "1Mi"},
		"negative gomaxprocs":      {"GOFI_GOMAXPROCS": "-1"},
		"metrics interval too low": {"GOFI_METRICS_INTERVAL": "100ms"},
		"batch bytes too small":    {"GOFI_SINK_BATCH_BYTES": "512"},
		"too many retries":         {"GOFI_SINK_RETRIES": "101"},
		"idle conns zero":          {"GOFI_HTTP_MAX_IDLE_CONNS": "0"},
		"decode budget tiny":       {"GOFI_MAX_DECODED_BYTES_PER_MESSAGE": "100"},
		"per-client > total":       {"GOFI_WATCH_MAX_STREAMS_PER_CLIENT": "1000"},
		"watch queue zero":         {"GOFI_WATCH_QUEUE": "0"},
		"hub resync zero":          {"GOFI_HUB_RESYNC": "0"},
	}
	for name, vars := range cases {
		_, err := LoadFrom(env(vars))
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		var named bool
		for k := range vars {
			if strings.Contains(err.Error(), k) {
				named = true
			}
		}
		if !named {
			t.Errorf("%s: error should name the variable: %v", name, err)
		}
	}
}

func TestByteSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "1024": 1024, "1k": 1 << 10, "1Ki": 1 << 10, "1KiB": 1 << 10, "64M": 64 << 20, "64Mi": 64 << 20, "64MB": 64 << 20, "2G": 2 << 30, "1T": 1 << 40, " 3 mi ": 3 << 20} {
		got, err := byteSize(in)
		if err != nil || got != want {
			t.Errorf("byteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "-1", "1.5M", "9999999999999999999G"} {
		if _, err := byteSize(in); err == nil {
			t.Errorf("byteSize(%q) should fail", in)
		}
	}
	for n, want := range map[int64]string{1 << 20: "1Mi", 3 << 10: "3Ki", 1536: "1536", 4 << 30: "4Gi", 0: "0"} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestGetSetReset(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	got := Get()
	if got.Profile != ProfileDefault && got.Profile != ProfileEdge && got.Profile != ProfileLarge {
		t.Fatalf("Get returned unloaded config: %+v", got)
	}
	custom := Preset(ProfileLarge)
	custom.Limits.MaxFrameBytes = 12345
	Set(custom)
	if Get().Limits.MaxFrameBytes != 12345 {
		t.Fatal("Set did not take effect")
	}
}

func TestFieldsDocumentedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range fields {
		if !strings.HasPrefix(f.Env, "GOFI_") {
			t.Errorf("%s: variables must be GOFI_-prefixed", f.Env)
		}
		if seen[f.Env] {
			t.Errorf("%s declared twice", f.Env)
		}
		seen[f.Env] = true
		if f.Desc == "" || f.get == nil || f.set == nil {
			t.Errorf("%s: incomplete field", f.Env)
		}
		for _, p := range []Profile{ProfileEdge, ProfileDefault, ProfileLarge} {
			if f.Default(p) == "" {
				t.Errorf("%s: empty default for %s", f.Env, p)
			}
		}
	}
	// Every default renders back into itself: the documented value is a
	// value users can paste into the environment.
	for _, p := range []Profile{ProfileEdge, ProfileDefault, ProfileLarge} {
		want := Preset(p)
		vars := map[string]string{}
		for _, f := range fields {
			v := f.Default(p)
			if v == `""` {
				continue
			}
			vars[f.Env] = v
		}
		got, err := LoadFrom(env(vars))
		if err != nil {
			t.Fatalf("%s: round trip: %v", p, err)
		}
		if got != want {
			t.Errorf("%s: round trip mismatch:\n got %+v\nwant %+v", p, got, want)
		}
	}
}

// TestDocsInSync keeps docs/configuration.md identical to MarkdownTable.
func TestDocsInSync(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "configuration.md"))
	if err != nil {
		t.Skip("docs not present:", err)
	}
	table := MarkdownTable()
	if !strings.Contains(string(doc), table) {
		t.Fatalf("docs/configuration.md is out of date; replace the variable table with:\n\n%s", table)
	}
}

func TestApplyFromCgroup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("134217728\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cpu.max"), []byte("50000 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Preset(ProfileEdge)
	prev := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })
	out := applyWith(cfg, func(string) string { return "" }, root, os.Stderr)
	if out.MemoryLimitBytes != 128<<20 || out.MemorySource != "cgroup v2 memory.max" {
		t.Errorf("memory discovery: %+v", out)
	}
	if want := int64(float64(int64(128<<20)) * cfg.Memory.GOMEMLIMITFraction); out.GOMEMLIMIT != want {
		t.Errorf("GOMEMLIMIT = %d, want %d", out.GOMEMLIMIT, want)
	}
	if out.GOMAXPROCS != 1 {
		t.Errorf("GOMAXPROCS = %d, want 1 from 0.5 CPU quota", out.GOMAXPROCS)
	}

	// Explicit GOMEMLIMIT in the environment wins; unlimited cgroup leaves it alone.
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("max\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = applyWith(cfg, func(k string) string {
		if k == "GOMAXPROCS" {
			return "4"
		}
		return ""
	}, root, os.Stderr)
	if out.GOMEMLIMIT != 0 || out.MemoryLimitBytes != 0 {
		t.Errorf("unlimited cgroup should not set GOMEMLIMIT: %+v", out)
	}
	if out.GOMAXPROCS != 1 {
		// GOMAXPROCS env set: we must not override the runtime's choice (still 1 from above).
		t.Errorf("GOMAXPROCS changed under explicit env: %d", out.GOMAXPROCS)
	}

	// Explicit GOFI_MEMORY_LIMIT is used when the cgroup is unlimited.
	cfg.Memory.LimitBytes = 64 << 20
	out = applyWith(cfg, func(string) string { return "" }, root, os.Stderr)
	if out.MemorySource != "GOFI_MEMORY_LIMIT" || out.GOMEMLIMIT != int64(float64(cfg.Memory.LimitBytes)*cfg.Memory.GOMEMLIMITFraction) {
		t.Errorf("explicit limit: %+v", out)
	}
}

func TestCgroupV1(t *testing.T) {
	root := t.TempDir()
	must := func(p, v string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "memory", "memory.limit_in_bytes"), "268435456")
	must(filepath.Join(root, "cpu", "cpu.cfs_quota_us"), "150000")
	must(filepath.Join(root, "cpu", "cpu.cfs_period_us"), "100000")
	if n, src := cgroupMemoryLimit(root); n != 256<<20 || !strings.Contains(src, "v1") {
		t.Errorf("v1 memory: %d %q", n, src)
	}
	if q := cgroupCPUQuota(root); q != 2 {
		t.Errorf("v1 cpu quota = %d, want 2", q)
	}
	// v1 "unlimited" is a huge number; must read as 0.
	must(filepath.Join(root, "memory", "memory.limit_in_bytes"), "9223372036854771712")
	if n, _ := cgroupMemoryLimit(root); n != 0 {
		t.Errorf("v1 unlimited should be 0, got %d", n)
	}
}
