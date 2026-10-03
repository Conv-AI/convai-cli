package credential

import "strings"

var credentialPrefixes = []string{"cvpat_", "cvoat_", "cvort_", "cvoac_", "ck_live_", "sk_"}

// Redact removes exact known secrets and any credential-shaped value from
// text. Callers must redact before truncating, so a cut cannot expose a prefix
// of a secret that the full-text match would have caught.
func Redact(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	for _, prefix := range credentialPrefixes {
		for {
			index := strings.Index(message, prefix)
			if index < 0 {
				break
			}
			end := index
			for end < len(message) && !strings.ContainsRune(" \t\r\n\"'()[]{}", rune(message[end])) {
				end++
			}
			message = message[:index] + "[REDACTED]" + message[end:]
		}
	}
	return message
}
