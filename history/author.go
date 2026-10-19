package history

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// identityHeaders are set by authenticating proxies in front of the portal
// (oauth2-proxy, Pomerium, and similar), most specific first.
var identityHeaders = []string{
	"X-Auth-Request-Preferred-Username",
	"X-Forwarded-Preferred-Username",
	"X-Auth-Request-User",
	"X-Forwarded-User",
	"X-Auth-Request-Email",
	"X-Forwarded-Email",
}

// AuthorFromHeader names the person behind a request for revision
// attribution, or UnknownAuthor.
//
// Under UDS Core, Authservice authenticates the portal session and forwards
// the OIDC ID token as "Authorization: Bearer <JWT>"; its preferred_username
// (then email, name, sub) claim names the author. The token's signature is
// not checked here: the value only labels a revision and grants nothing. It
// is trustworthy only when an authenticating proxy owns these headers and
// replaces client-supplied values; without one, any client can claim any
// name. docs/versioning.md.
func AuthorFromHeader(header http.Header) string {
	for _, name := range identityHeaders {
		if value := clean(header.Get(name)); value != "" {
			return value
		}
	}
	if token, ok := strings.CutPrefix(header.Get("Authorization"), "Bearer "); ok {
		if claims := jwtClaims(strings.TrimSpace(token)); claims != nil {
			for _, key := range []string{"preferred_username", "email", "name", "sub"} {
				if value, _ := claims[key].(string); clean(value) != "" {
					return clean(value)
				}
			}
		}
	}
	return UnknownAuthor
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > 16<<10 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

// clean keeps an identity printable and bounded.
func clean(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) > 256 {
		value = strings.ToValidUTF8(value[:256], "")
	}
	return value
}

// CleanMessage bounds a revision message (one paragraph of text).
func CleanMessage(message string) string {
	message = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, message))
	if len(message) > 1024 {
		message = strings.ToValidUTF8(message[:1024], "")
	}
	return message
}

// CopyIdentity copies only the headers AuthorFromHeader reads from src to
// dst, so an in-process call made on a user's behalf carries their identity
// and nothing else (cookies, forwarding headers).
func CopyIdentity(dst, src http.Header) {
	for _, name := range append(append([]string{}, identityHeaders...), "Authorization") {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}
