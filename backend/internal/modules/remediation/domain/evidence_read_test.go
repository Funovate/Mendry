package domain_test

import (
	"context"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func validEvidenceReadRequest() domain.EvidenceReadRequest {
	return domain.EvidenceReadRequest{
		RunID:      "0190-0000-0000-7000-000000000001",
		EvidenceID: "0190-0000-0000-7000-000000000002",
		Cursor:     "",
	}
}

func validEvidenceReadPage() domain.EvidenceReadPage {
	return domain.EvidenceReadPage{
		EvidenceID:           "0190-0000-0000-7000-000000000002",
		Kind:                 domain.EvidenceKindProviderDetail,
		StoredClassification: domain.EvidenceDirectFault,
		Provenance: domain.EvidenceReadProvenance{
			SourceID:      "0190-0000-0000-7000-000000000003",
			SourceAttempt: 1,
		},
		ContentHash: strings.Repeat("a", 64),
		Content:     `{"record":"trusted detail"}`,
		Truncated:   false,
		ByteCount:   26,
	}
}

// TestEvidenceReadRequestValidate 覆盖 evidence.read 输入的边界：身份必填、
// 长度受限，cursor 可选且受字节上限约束。
func TestEvidenceReadRequestValidate(t *testing.T) {
	t.Run("valid first page request", func(t *testing.T) {
		if err := validEvidenceReadRequest().Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("valid continuation with cursor", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.Cursor = "opaque-token"
		if err := request.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("missing run identity is rejected", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.RunID = ""
		if err := request.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("missing evidence identity is rejected", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.EvidenceID = "  "
		if err := request.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("oversized identities are rejected", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.RunID = strings.Repeat("r", 129)
		if err := request.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection for run identity")
		}
		request = validEvidenceReadRequest()
		request.EvidenceID = strings.Repeat("e", 129)
		if err := request.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection for evidence identity")
		}
	})

	t.Run("oversized cursor is rejected", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.Cursor = strings.Repeat("x", domain.MaxEvidenceReadCursorBytes+1)
		if err := request.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("cursor at the bound is accepted", func(t *testing.T) {
		request := validEvidenceReadRequest()
		request.Cursor = strings.Repeat("x", domain.MaxEvidenceReadCursorBytes)
		if err := request.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

// TestEvidenceReadPageValidate 覆盖页面输出的完整性边界：身份、分类、hash、
// 字节数、content 上限，以及 truncated 与 nextCursor 的一致性。
func TestEvidenceReadPageValidate(t *testing.T) {
	t.Run("valid complete page", func(t *testing.T) {
		if err := validEvidenceReadPage().Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("valid truncated page carries a next cursor", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.Truncated = true
		page.NextCursor = "opaque-token"
		page.ByteCount = domain.MaxEvidenceReadPageBytes
		page.Content = strings.Repeat("c", domain.MaxEvidenceReadPageBytes)
		if err := page.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("truncated page without a next cursor is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.Truncated = true
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("complete page with a next cursor is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.NextCursor = "opaque-token"
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("invalid stored classification is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.StoredClassification = domain.EvidenceClassification("model_supplied")
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("malformed content hash is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.ContentHash = strings.Repeat("a", 63)
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("negative or oversized byte count is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.ByteCount = -1
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection for negative bytes")
		}
		page = validEvidenceReadPage()
		page.ByteCount = domain.MaxEvidenceReadPageBytes + 1
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection for oversized bytes")
		}
	})

	t.Run("content over the page bound is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.Content = strings.Repeat("c", domain.MaxEvidenceReadPageBytes+1)
		page.ByteCount = int64(len(page.Content))
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("oversized next cursor is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.Truncated = true
		page.NextCursor = strings.Repeat("x", domain.MaxEvidenceReadCursorBytes+1)
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
	})

	t.Run("missing identity is rejected", func(t *testing.T) {
		page := validEvidenceReadPage()
		page.EvidenceID = ""
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection")
		}
		page = validEvidenceReadPage()
		page.Kind = ""
		if err := page.Validate(); err == nil {
			t.Fatal("Validate() error = nil, want rejection for missing kind")
		}
	})
}

// TestEvidenceReadPortContract 编译期断言 EvidenceReadPort 是可注入的窄端口，
// 并且 sentinel 错误是独立的稳定值（gateway 按 errors.Is 映射）。
func TestEvidenceReadPortContract(t *testing.T) {
	var port domain.EvidenceReadPort = fakeEvidenceReadPort{}
	if port == nil {
		t.Fatal("fake evidence read port must satisfy the port")
	}
	if domain.ErrEvidenceReadNotFound == domain.ErrEvidenceReadCursorInvalid ||
		domain.ErrEvidenceReadCursorInvalid == domain.ErrEvidenceReadCursorExpired {
		t.Fatal("evidence read sentinel errors must be distinct")
	}
}

type fakeEvidenceReadPort struct{}

func (fakeEvidenceReadPort) ReadEvidence(ctx context.Context, request domain.EvidenceReadRequest) (domain.EvidenceReadPage, error) {
	return domain.EvidenceReadPage{}, domain.ErrEvidenceReadNotFound
}
