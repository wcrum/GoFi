package codec

import (
	"errors"
	"fmt"

	"github.com/wcrum/gofi/config"
	"github.com/wcrum/gofi/message"
)

// Per-message decoded-bytes budget. Each decoder step's output is capped on
// its own (max, 1 MiB by default), but a flow may decode in the decode step
// and again in any number of parse stages. The budget bounds the sum of all
// decoder output for one message, so a hostile 100-byte frame cannot expand
// into tens of MiB per stage. Exceeding it is not a parse error to continue
// past: the pipeline drops the message (ErrBudgetExceeded).
//
// The budget is config.Limits.MaxDecodedBytesPerMessage
// (GOFI_MAX_DECODED_BYTES_PER_MESSAGE, 8 MiB in the default profile).

// ErrBudgetExceeded marks a decode that would exceed the message budget.
var ErrBudgetExceeded = errors.New("decoded bytes exceed the per-message budget")

// MessageBudget is the configured per-message decoded-bytes budget.
func MessageBudget() int { return config.Get().Limits.MaxDecodedBytesPerMessage }

// charge adds n decoded bytes to msg's total and fails once the budget is
// exceeded. A nil msg (a standalone decode, Simulate's scratch decodes) is
// bounded only by each step's own cap.
func charge(msg *message.Message, n int) error {
	if msg == nil {
		return nil
	}
	msg.DecodedBytes += n
	if budget := MessageBudget(); msg.DecodedBytes > budget {
		return fmt.Errorf("%w: %d bytes decoded so far, budget %d", ErrBudgetExceeded, msg.DecodedBytes, budget)
	}
	return nil
}

// IsBudgetExceeded reports whether err is (or wraps) ErrBudgetExceeded.
func IsBudgetExceeded(err error) bool { return errors.Is(err, ErrBudgetExceeded) }
