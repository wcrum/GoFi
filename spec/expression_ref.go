package spec

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// UnmarshalJSON accepts generation as a JSON number (files, CRDs) or as a
// decimal string (protobuf JSON encodes int64 values as strings).
func (r *ExpressionRef) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name       string          `json:"name"`
		Generation json.RawMessage `json:"generation"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Name, r.Generation = raw.Name, 0
	if len(raw.Generation) == 0 || string(raw.Generation) == "null" {
		return nil
	}
	text := string(raw.Generation)
	if unquoted, err := strconv.Unquote(text); err == nil {
		text = unquoted
	}
	generation, err := strconv.ParseInt(text, 10, 64)
	if err != nil || generation < 0 {
		return fmt.Errorf("expressionRef.generation must be a non-negative integer, got %s", raw.Generation)
	}
	r.Generation = generation
	return nil
}
