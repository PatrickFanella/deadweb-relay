package safety

import (
	"regexp"
	"strings"
)

var patterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH |PGP )?PRIVATE KEY-----`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"openai-style-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)},
	{"aws-access-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"generic-secret", regexp.MustCompile(`(?i)\b(api[_-]?key|secret|password|passwd|token)\s*[:=]\s*["']?[A-Za-z0-9._~+/=-]{16,}`)},
}

func CredentialLike(s string) (string, bool) {
	for _, p := range patterns {
		if p.re.FindStringIndex(s) != nil {
			return p.name, true
		}
	}
	return "", false
}

func SafeTextContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "text/plain", "text/markdown", "text/csv", "application/json", "application/xml", "text/xml", "image/svg+xml":
		return true
	default:
		return false
	}
}
