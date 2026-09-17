package application

import (
	"context"
	"mendry/backend/internal/modules/projects/domain"
)

// HotfixPreparationPort prepares and tests a credential-free validation environment.
// Implementations must never execute repository commands on the API host.
type HotfixPreparationPort interface {
	Prepare(context.Context, HotfixPreparationRequest, func(string)) (HotfixPreparationResult, error)
}

type HotfixPreparationRequest struct {
	ProjectID            string
	CheckID              string
	Repository           domain.Repository
	GitCredential        domain.Secret
	RepositoryCredential domain.Secret
	Directory            string
}

type HotfixCandidate struct {
	Directory string `json:"directory"`
	Runtime   string `json:"runtime"`
}

type HotfixPreparationResult struct {
	Candidates        []HotfixCandidate `json:"candidates"`
	Directory         string            `json:"directory"`
	Runtime           string            `json:"runtime"`
	BaselineCommit    string            `json:"baselineCommit"`
	ValidationSummary string            `json:"validationSummary"`
	BranchOnly        bool              `json:"branchOnly"`
	// Policy stays server-side until explicitly enabled after successful checks.
	Policy domain.RemediationPolicy `json:"-"`
}
