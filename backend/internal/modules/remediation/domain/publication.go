package domain

import "context"

type CapabilityStatus string

const (
	CapabilitySupported   CapabilityStatus = "supported"
	CapabilityUnsupported CapabilityStatus = "unsupported"
	CapabilityUnknown     CapabilityStatus = "unknown"
)

type ProviderCapabilities struct {
	Status         CapabilityStatus
	DraftSupported bool
	RepositoryID   string
	APIBaseURL     string
	ReasonCode     string
}

type GitPublicationRequest struct {
	PublicationRequest
	AuthorName  string
	AuthorEmail string
	AuthoredAt  string
}

type GitPublicationResult struct {
	BranchRef        string
	CommitHash       string
	BaselineCommit   string
	TargetBranch     string
	ExpectedTreeHash string
	AlreadyPublished bool
	RemoteVerified   bool
	TargetDiverged   bool
	Summary          string
}

type GitPublicationPort interface {
	PublishGit(context.Context, GitPublicationRequest) (GitPublicationResult, error)
}

type ChangeRequestLookup struct {
	ProjectID           string
	RepositoryID        string
	SourceBranch        string
	TargetBranch        string
	PublicationSnapshot PublicationSnapshot
}

type ChangeRequestRequest struct {
	ChangeRequestLookup
	Title          string
	Body           string
	Draft          bool
	CommitHash     string
	IdempotencyKey string
}

type ChangeRequestResult struct {
	Status        CapabilityStatus
	Reference     string
	URL           string
	Draft         bool
	AlreadyExists bool
	ReasonCode    string
}

type ChangeRequestPort interface {
	ProbeCapabilities(context.Context, string, PublicationSnapshot) (ProviderCapabilities, error)
	FindChangeRequest(context.Context, ChangeRequestLookup) (ChangeRequestResult, error)
	CreateChangeRequest(context.Context, ChangeRequestRequest) (ChangeRequestResult, error)
}
