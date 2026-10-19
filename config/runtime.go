package config

import (
	"log/slog"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// Applied reports what Apply changed, for logging and tests.
type Applied struct {
	MemoryLimitBytes int64 // budget discovered (0 = none)
	MemorySource     string
	GOMEMLIMIT       int64 // value handed to the GC (0 = untouched)
	GOMAXPROCS       int   // value in effect after Apply
	LogLevel         slog.Level
}

// Apply installs process-wide effects of cfg: the slog default handler,
// GOMEMLIMIT (unless the GOMEMLIMIT env var is already set) and GOMAXPROCS.
// It is safe to call once at startup before goroutines fan out.
func Apply(cfg Config) Applied {
	return applyWith(cfg, os.Getenv, "/sys/fs/cgroup", os.Stdout)
}

func applyWith(cfg Config, getenv func(string) string, cgroupRoot string, logOut *os.File) Applied {
	var out Applied

	// Logging first so everything below is reported in the chosen format.
	opts := &slog.HandlerOptions{Level: cfg.Observability.LogLevel}
	var h slog.Handler
	if cfg.Observability.LogFormat == "text" {
		h = slog.NewTextHandler(logOut, opts)
	} else {
		h = slog.NewJSONHandler(logOut, opts)
	}
	slog.SetDefault(slog.New(h))
	out.LogLevel = cfg.Observability.LogLevel

	// Memory: explicit GOMEMLIMIT wins; otherwise GOFI_MEMORY_LIMIT, then cgroup.
	if getenv("GOMEMLIMIT") == "" {
		limit, src := cfg.Memory.LimitBytes, "GOFI_MEMORY_LIMIT"
		if limit <= 0 {
			limit, src = cgroupMemoryLimit(cgroupRoot)
		}
		if limit > 0 {
			out.MemoryLimitBytes, out.MemorySource = limit, src
			soft := int64(float64(limit) * cfg.Memory.GOMEMLIMITFraction)
			if soft < 8<<20 {
				soft = 8 << 20
			}
			debug.SetMemoryLimit(soft)
			out.GOMEMLIMIT = soft
		}
	}

	// CPU: explicit override, else cgroup quota when the runtime has not
	// already applied it (Go >= 1.25 does this itself; older runtimes report
	// the host core count, which starves a quota-limited container).
	if cfg.Memory.GOMAXPROCS > 0 {
		runtime.GOMAXPROCS(cfg.Memory.GOMAXPROCS)
	} else if getenv("GOMAXPROCS") == "" {
		if q := cgroupCPUQuota(cgroupRoot); q > 0 && q < runtime.GOMAXPROCS(0) {
			runtime.GOMAXPROCS(q)
		}
	}
	out.GOMAXPROCS = runtime.GOMAXPROCS(0)
	return out
}

// cgroupMemoryLimit returns the cgroup v2 memory.max or v1
// memory.limit_in_bytes of the current process, or 0 when unlimited/unknown.
func cgroupMemoryLimit(root string) (int64, string) {
	if n := readCgroupInt(root + "/memory.max"); n > 0 {
		return n, "cgroup v2 memory.max"
	}
	if n := readCgroupInt(root + "/memory/memory.limit_in_bytes"); n > 0 && n < math.MaxInt64/2 {
		return n, "cgroup v1 memory.limit_in_bytes"
	}
	return 0, ""
}

// cgroupCPUQuota returns ceil(quota/period) from cgroup v2 cpu.max or v1
// cpu.cfs_quota_us/cpu.cfs_period_us, or 0 when unlimited/unknown.
func cgroupCPUQuota(root string) int {
	if b, err := os.ReadFile(root + "/cpu.max"); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) == 2 && parts[0] != "max" {
			q, err1 := strconv.ParseFloat(parts[0], 64)
			p, err2 := strconv.ParseFloat(parts[1], 64)
			if err1 == nil && err2 == nil && p > 0 && q > 0 {
				return int(math.Ceil(q / p))
			}
		}
		return 0
	}
	q := readCgroupInt(root + "/cpu/cpu.cfs_quota_us")
	p := readCgroupInt(root + "/cpu/cpu.cfs_period_us")
	if q > 0 && p > 0 {
		return int(math.Ceil(float64(q) / float64(p)))
	}
	return 0
}

func readCgroupInt(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "max" || s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
