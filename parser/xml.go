package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/wcrum/gofi/message"
)

type xmlParser struct{}

// Parse extracts the root element's attributes and immediate child element
// text content into Fields. Nested structures are flattened with dot notation.
func (p *xmlParser) Parse(msg *message.Message) error {
	dec := xml.NewDecoder(bytes.NewReader(msg.Raw))

	var path []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			for _, attr := range t.Attr {
				key := strings.Join(path, ".") + "." + attr.Name.Local
				msg.Fields[key] = attr.Value
			}
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) == 0 {
				continue
			}
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			key := strings.Join(path, ".")
			if existing, ok := msg.Fields[key]; ok {
				msg.Fields[key] = fmt.Sprintf("%v %s", existing, text)
			} else {
				msg.Fields[key] = text
			}
		}
	}
	return nil
}
