//go:build gofi_nosql

package source

import (
	"context"
	"errors"

	"github.com/wcrum/gofi/spec"
)

// ErrSQLDisabled is returned when the binary was built with -tags gofi_nosql
// (edge builds drop the Postgres, MySQL and SQL Server drivers to save
// several MB of binary and heap).
var ErrSQLDisabled = errors.New("sql source: not available in this build (compiled with gofi_nosql)")

func runSQL(context.Context, string, *spec.FetchSource, Emit) error { return ErrSQLDisabled }

func sqlOrigin(cfg *spec.SQLFetch) string {
	if cfg == nil {
		return "sql"
	}
	return cfg.Driver + " (disabled in this build)"
}
