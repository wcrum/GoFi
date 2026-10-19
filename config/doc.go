package config

import (
	"fmt"
	"strings"
)

// MarkdownTable renders the variable reference used in docs/configuration.md.
// The columns are: variable, default preset value, edge preset, large preset,
// effect. TestDocsInSync fails when the file drifts from this output.
func MarkdownTable() string {
	var b strings.Builder
	b.WriteString("| Variable | Default | Edge | Large | Effect |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | %s |\n",
			f.Env, f.Default(ProfileDefault), f.Default(ProfileEdge), f.Default(ProfileLarge), f.Desc)
	}
	return b.String()
}
