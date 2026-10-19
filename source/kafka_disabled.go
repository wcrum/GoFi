//go:build gofi_nokafka

package source

import (
	"context"
	"errors"

	"github.com/wcrum/gofi/spec"
)

// ErrKafkaDisabled is returned when the binary was built with
// -tags gofi_nokafka (edge builds drop the franz-go client).
var ErrKafkaDisabled = errors.New("kafka source: not available in this build (compiled with gofi_nokafka)")

func runKafka(context.Context, string, *spec.KafkaSubscribe, Emit) error { return ErrKafkaDisabled }
