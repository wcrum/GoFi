package filter

import (
	"fmt"
	"regexp"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// regexFilter uses Go's RE2-compatible engine with linear-time matching.
// It follows the chain's fail-open convention for missing/non-string fields.
type regexFilter struct {
	pattern *regexp.Regexp
	field   string
}

func newRegex(cfg spec.StageSpec) (Stage, error) {
	if cfg.Expression == "" {
		return nil, fmt.Errorf("regex: expression must be non-empty")
	}
	pattern, err := regexp.Compile(cfg.Expression)
	if err != nil {
		return nil, fmt.Errorf("regex: %w", err)
	}
	return &regexFilter{pattern: pattern, field: cfg.Field}, nil
}

func (f *regexFilter) Process(msg *message.Message) (bool, error) {
	if f.field == "" {
		return f.pattern.Match(msg.Raw), nil
	}
	value, exists := msg.Fields[f.field]
	if !exists {
		return true, fmt.Errorf("regex: missing field %q", f.field)
	}
	text, ok := value.(string)
	if !ok {
		return true, fmt.Errorf("regex: field %q must be a string", f.field)
	}
	return f.pattern.MatchString(text), nil
}
