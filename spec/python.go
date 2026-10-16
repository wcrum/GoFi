package spec

import "fmt"

// PythonRef identifies a named function in a durable, isolated Python library.
// Runtime addresses and credentials are resolved privately by the controller.
type PythonRef struct {
	Library  string `json:"library"`
	Function string `json:"function"`
}

func (r *PythonRef) Validate() error {
	if r == nil || r.Library == "" || r.Function == "" {
		return fmt.Errorf("python.library and python.function are required")
	}
	return nil
}

func (d ListenerDefinition) UsesPython() bool {
	if d.Source.Python != nil || d.Parser.Python != nil {
		return true
	}
	for _, s := range d.Stages {
		if s.Python != nil {
			return true
		}
	}
	for _, s := range d.Sinks {
		if s.Python != nil {
			return true
		}
	}
	return d.DeadLetterSink != nil && d.DeadLetterSink.Python != nil
}
