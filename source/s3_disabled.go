//go:build gofi_nos3

package source

import (
	"context"
	"errors"

	"github.com/wcrum/gofi/spec"
)

// ErrS3Disabled is returned when the binary was built with -tags gofi_nos3
// (edge builds drop the MinIO client).
var ErrS3Disabled = errors.New("s3 source: not available in this build (compiled with gofi_nos3)")

func runS3(context.Context, string, *spec.FetchSource, Emit) error { return ErrS3Disabled }
