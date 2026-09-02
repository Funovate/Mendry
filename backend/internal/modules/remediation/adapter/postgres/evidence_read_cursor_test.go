package postgres

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestEvidenceReadCursorTokenIsRandomOpaqueCapability(t *testing.T) {
	first, firstDigest, err := newEvidenceReadCursorToken()
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := newEvidenceReadCursorToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || bytes.Equal(firstDigest, secondDigest) {
		t.Fatal("independently minted cursor capabilities must differ")
	}
	if len(first) > domain.MaxEvidenceReadCursorBytes || len(firstDigest) != 32 {
		t.Fatalf("cursor/digest bounds = %d/%d", len(first), len(firstDigest))
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != 32 {
		t.Fatalf("cursor must be one unstructured 256-bit value: %v, %d", err, len(raw))
	}
	for _, visible := range []string{"evidenceId", "contentHash", "offset", "expiresAt", "4096"} {
		if strings.Contains(first, visible) {
			t.Fatalf("cursor leaks model-visible field %q", visible)
		}
	}
}
