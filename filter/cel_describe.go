package filter

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/wcrum/gofi/message"
)

// Diagnostic is one compile or type-check issue with a 1-based location.
type Diagnostic struct {
	Line    int
	Column  int
	Message string
}

// ResultTypeBool and ResultTypeAny are the library result types: bool for
// filters and sink conditions, any for transform values and dedup keys.
const (
	ResultTypeBool = "bool"
	ResultTypeAny  = "any"
)

// ValidResultType reports whether t is a supported library result type.
func ValidResultType(t string) bool { return t == ResultTypeBool || t == ResultTypeAny }

// CompileFor compiles expression for a result type using the runtime rules:
// bool requires a boolean (or dynamically typed) output.
func CompileFor(expression, resultType string) (cel.Program, error) {
	if resultType == ResultTypeBool {
		return CompilePredicate(expression)
	}
	return CompileCEL(expression)
}

// Check compiles expression in the shared environment and returns the
// checked output type plus diagnostics, without evaluating anything.
func Check(expression, resultType string) (string, []Diagnostic) {
	if len(expression) > MaxExpressionBytes {
		return "", []Diagnostic{{Line: 1, Column: 1, Message: fmt.Sprintf("expression exceeds %d bytes", MaxExpressionBytes)}}
	}
	if strings.TrimSpace(expression) == "" {
		return "", []Diagnostic{{Line: 1, Column: 1, Message: "expression is empty"}}
	}
	env, err := Env()
	if err != nil {
		return "", []Diagnostic{{Line: 1, Column: 1, Message: err.Error()}}
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		var out []Diagnostic
		for _, e := range issues.Errors() {
			out = append(out, Diagnostic{Line: max(e.Location.Line(), 1), Column: e.Location.Column() + 1, Message: e.Message})
		}
		return "", out
	}
	outputType := ast.OutputType().String()
	if resultType == ResultTypeBool && !ast.OutputType().IsExactType(cel.BoolType) && !ast.OutputType().IsExactType(cel.DynType) {
		return outputType, []Diagnostic{{Line: 1, Column: 1, Message: "expression must return a bool, but returns " + outputType}}
	}
	return outputType, nil
}

// Variable and Function describe the shared environment for editors.
type Variable struct{ Name, Type, Description string }
type Function struct {
	Name        string
	Signatures  []string
	Description string
}

var variableDescriptions = map[string]string{
	"fields": "Parsed message fields (JSON keys, XML elements, or Python parser output).",
	"meta":   "Pipeline metadata such as source, remoteAddr, ts, and parseError.",
	"raw":    "The framed message as a UTF-8 string, before decoding.",
}

// identifier matches callable function names (operators are omitted).
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// Describe lists the variables and named functions of the shared environment.
func Describe() ([]Variable, []Function, error) {
	env, err := Env()
	if err != nil {
		return nil, nil, err
	}
	var variables []Variable
	for _, v := range env.Variables() {
		variables = append(variables, Variable{Name: v.Name(), Type: v.Type().String(), Description: variableDescriptions[v.Name()]})
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	var functions []Function
	for name, decl := range env.Functions() {
		if !identifier.MatchString(name) || decl.IsDeclarationDisabled() {
			continue
		}
		fn := Function{Name: name, Description: decl.Description()}
		for _, o := range decl.OverloadDecls() {
			args := o.ArgTypes()
			names := make([]string, 0, len(args))
			for _, a := range args {
				names = append(names, a.String())
			}
			signature := fmt.Sprintf("%s(%s) -> %s", name, strings.Join(names, ", "), o.ResultType().String())
			if o.IsMemberFunction() && len(names) > 0 {
				signature = fmt.Sprintf("%s.%s(%s) -> %s", names[0], name, strings.Join(names[1:], ", "), o.ResultType().String())
			}
			fn.Signatures = append(fn.Signatures, signature)
		}
		sort.Strings(fn.Signatures)
		functions = append(functions, fn)
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	return variables, functions, nil
}

// EvaluateJSON evaluates program against msg under the runtime deadline and
// cost limit, returning the result as a JSON value plus the actual cost.
func EvaluateJSON(ctx context.Context, program cel.Program, msg *message.Message) (*structpb.Value, uint64, error) {
	activation := map[string]interface{}{"fields": msg.Fields, "meta": msg.Meta, "raw": string(msg.Raw)}
	ctx, cancel := context.WithTimeout(ctx, EvalTimeout)
	defer cancel()
	out, details, err := program.ContextEval(ctx, activation)
	var cost uint64
	if details != nil && details.ActualCost() != nil {
		cost = *details.ActualCost()
	}
	if err != nil {
		return nil, cost, err
	}
	native, err := out.ConvertToNative(reflect.TypeOf(&structpb.Value{}))
	if err != nil {
		return nil, cost, fmt.Errorf("result of type %s is not representable as JSON: %w", out.Type().TypeName(), err)
	}
	value, ok := native.(*structpb.Value)
	if !ok {
		return nil, cost, fmt.Errorf("result of type %s is not representable as JSON", out.Type().TypeName())
	}
	return value, cost, nil
}
