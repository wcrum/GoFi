package driver

import (
	"os"
	"strconv"
)

// envBool reads a boolean feature switch set by the Helm chart.
func envBool(name string, fallback bool) bool {
	if value, err := strconv.ParseBool(os.Getenv(name)); err == nil {
		return value
	}
	return fallback
}
