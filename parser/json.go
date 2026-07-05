package parser

import (
	"encoding/json"
	"fmt"

	"github.com/wcrum/gofi/message"
)

type jsonParser struct{}

func (p *jsonParser) Parse(msg *message.Message) error {
	var top interface{}
	if err := json.Unmarshal(msg.Raw, &top); err != nil {
		return fmt.Errorf("parser: json unmarshal: %w", err)
	}
	flattenInto(msg.Fields, top, "")
	return nil
}

func flattenInto(dst map[string]interface{}, v interface{}, prefix string) {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(dst, child, key)
		}
	case []interface{}:
		dst[prefix] = val
	default:
		dst[prefix] = val
	}
}
