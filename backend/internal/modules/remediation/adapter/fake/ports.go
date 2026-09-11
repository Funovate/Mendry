package fake

import (
	"context"

	"mendry/backend/internal/modules/remediation/domain"
)

// Repository 是仓库只读占位适配器，供测试使用。
type Repository struct{}

func (Repository) ListTree(context.Context, domain.RepoRef, string, domain.TreeOptions) (domain.TreeListing, error) {
	return domain.TreeListing{}, nil
}
func (Repository) ReadFile(context.Context, domain.RepoRef, string, domain.ReadOptions) (domain.FileContent, error) {
	return domain.FileContent{}, nil
}
func (Repository) Search(context.Context, domain.RepoRef, domain.SearchQuery) (domain.SearchResult, error) {
	return domain.SearchResult{}, nil
}
func (Repository) History(context.Context, domain.RepoRef, string, domain.HistoryOptions) (domain.History, error) {
	return domain.History{}, nil
}

// Evidence 是证据只读占位适配器，供测试使用。
type Evidence struct{}

func (Evidence) Search(context.Context, domain.EvidenceScope, domain.LogQuery) (domain.EvidencePage, error) {
	return domain.EvidencePage{}, nil
}
func (Evidence) GetContext(context.Context, domain.EvidenceScope, domain.EvidenceAnchor) (domain.EvidencePage, error) {
	return domain.EvidencePage{}, nil
}

func (Evidence) Inspect(context.Context, domain.EvidenceScope, domain.SSHInspectRequest) (domain.SSHInspectResult, error) {
	return domain.SSHInspectResult{}, nil
}

// Model 是模型占位适配器，供测试使用。
type Model struct{}

func (Model) Complete(context.Context, domain.ModelTurn) (domain.ModelResult, error) {
	return domain.ModelResult{
		Content:      `{"schemaVersion":"v1","kind":"stop","stop":{"reason":"step5 placeholder model","recommendedNextAction":"ask an operator to review the incident"}}`,
		Provider:     "fake",
		Model:        "step5-placeholder",
		FinishReason: "stop",
	}, nil
}

var (
	_ domain.RepositoryReadPort = Repository{}
	_ domain.EvidenceLogPort    = Evidence{}
	_ domain.SSHInspectPort     = Evidence{}
	_ domain.LLMProviderPort    = Model{}
)
