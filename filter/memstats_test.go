package filter

import "runtime"

type runtime_MemStats = runtime.MemStats

func readTotalAlloc(m *runtime.MemStats) uint64 {
	runtime.ReadMemStats(m)
	return m.TotalAlloc
}
