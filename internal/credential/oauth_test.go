package credential

import (
	"testing"

	"github.com/Conv-AI/convai-cli/internal/config"
)

func TestOAuthAccessTokenClassificationAndNonAccessTokenRejection(t *testing.T) {
	t.Parallel()
	access, err := Parse("cvoat_01234567890123456789012345678901")
	if err != nil || access.Type != config.CredentialOAuth {
		t.Fatal("OAuth access token was not classified as bearer")
	}
	for _, value := range []string{"cvort_01234567890123456789012345678901", "cvoac_01234567890123456789012345678901", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJydW50aW1lIn0.signature"} {
		if _, err := Parse(value); err == nil {
			t.Fatal("non-authoring credential accepted")
		}
	}
}
