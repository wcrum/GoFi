package driver

import "strings"

// sanitizeName converts a listener name into a value safe for use as a
// container name or Kubernetes object name: lowercase, alphanumerics and
// '-' only, invalid characters replaced with '-', no leading/trailing '-'.
// Shared by the Docker and Kubernetes drivers so their generated
// container/Pod names follow the same convention.
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
