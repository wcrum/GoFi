package filter

import (
	"fmt"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// schemaStage drops any message missing one of RequiredFields. A missing
// field is expected/valid data, not a bug, so a drop is never an error.
type schemaStage struct {
	requiredFields []string
}

func newSchema(cfg spec.StageSpec) (Stage, error) {
	if len(cfg.RequiredFields) == 0 {
		return nil, fmt.Errorf("schema: requiredFields must be non-empty")
	}
	fields := make([]string, len(cfg.RequiredFields))
	copy(fields, cfg.RequiredFields)
	return &schemaStage{requiredFields: fields}, nil
}

func (s *schemaStage) Process(msg *message.Message) (bool, error) {
	for _, f := range s.requiredFields {
		if _, ok := msg.Fields[f]; !ok {
			return false, nil
		}
	}
	return true, nil
}
