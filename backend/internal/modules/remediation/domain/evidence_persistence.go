package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	EvidenceKindNormalizedAlert      = "normalized_alert"
	EvidenceKindProviderDetail       = "provider_detail"
	EvidenceKindConnectorObservation = "connector_observation"
	EvidenceKindRuntime              = "runtime"
)

// StoredEvidence is the adapter-neutral persisted evidence record. Payload is
// operational evidence only; credential/control material must be absent before
// this port is called.
type StoredEvidence struct {
	EvidenceID             string
	ProjectID              string
	EnvironmentID          string
	SourceID               string
	IncidentID             string
	RunID                  string
	ObservationID          string
	Provider               string
	EvidenceKind           string
	DeduplicationKey       string
	Classification         EvidenceClassification
	Outcome                string
	Available              bool
	Primary                bool
	TemporalCorrelation    bool
	OperationalCorrelation bool
	OccurredAt             *time.Time
	IngestedAt             time.Time
	ContentHash            string
	ByteCount              int64
	Provenance             json.RawMessage
	Payload                json.RawMessage
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// TriggeringObservation is the bounded, credential-free reference that links
// a remediation run back to the accepted inbound event. The raw webhook
// message is intentionally absent: provider adapters persist the safe
// normalized alert and operational evidence separately.
type TriggeringObservation struct {
	ID            string
	ProjectID     string
	EnvironmentID string
	SourceID      string
	Service       *string
	OccurredAt    time.Time
	Level         string
	Host          *string
	RequestID     *string
	Fingerprint   string
	Attributes    json.RawMessage
	IngestedAt    time.Time
}

// BootstrapEvidence is the pre-run evidence snapshot used to build the first
// diagnosis turn. Records are limited to the triggering Observation, while
// runtime evidence remains run-scoped and is collected through tools.
type BootstrapEvidence struct {
	Observation        *TriggeringObservation
	Records            []StoredEvidence
	SourceCoverage     []SourceCoverage
	AlertQuality       AlertQuality
	TimeRange          TimeRange
	OriginalTimeValues []string
	TimeBasis          string
	TimeCertainty      string
	MissingEvidence    []string
	Contradictions     []string
}

func (e StoredEvidence) Validate() error {
	for name, value := range map[string]string{
		"project": e.ProjectID, "environment": e.EnvironmentID, "source": e.SourceID,
		"incident": e.IncidentID, "provider": e.Provider, "evidence kind": e.EvidenceKind,
		"deduplication key": e.DeduplicationKey, "content hash": e.ContentHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := ValidateEvidenceClassification(e.Classification); err != nil {
		return err
	}
	if len(e.Provider) > 64 || len(e.EvidenceKind) > 96 || len(e.DeduplicationKey) > 255 || len(e.ContentHash) != 64 || e.ByteCount < 0 || e.ByteCount > 8<<20 {
		return fmt.Errorf("stored evidence bounds are invalid")
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) || len(e.Payload) > 8<<20 {
		return fmt.Errorf("stored evidence payload is invalid")
	}
	if len(e.Provenance) == 0 || !json.Valid(e.Provenance) || len(e.Provenance) > 64<<10 {
		return fmt.Errorf("stored evidence provenance is invalid")
	}
	return nil
}

// EvidenceAssessment is the durable service-owned decision applied after a
// model diagnosis. It is intentionally independent from Decision.Confidence.
type EvidenceAssessment struct {
	RunID               string
	ProjectID           string
	IncidentID          string
	ConfidenceCap       float64
	EffectiveConfidence float64
	PlanningEligible    bool
	Outcome             FixabilityClass
	Reasons             []string
	MissingEvidence     []string
	Contradictions      []string
	DirectEvidenceIDs   []string
	AssessedAt          time.Time
}

// EvidencePersistencePort owns append-only evidence and run-scoped gate
// assessment persistence. It does not expose database or HTTP types.
type EvidencePersistencePort interface {
	AppendEvidence(context.Context, StoredEvidence) (StoredEvidence, error)
	ResolveEvidence(context.Context, string, []EvidenceCitation) (EvidenceResolution, error)
	PersistEvidenceAssessment(context.Context, EvidenceAssessment) error
}

// BootstrapEvidenceLoader loads only evidence that belongs to the triggering
// inbound Observation. It is separate from citation resolution so the model
// cannot choose the initial context or use an evidence ID as an authority.
type BootstrapEvidenceLoader interface {
	LoadBootstrapEvidence(context.Context, string) (BootstrapEvidence, error)
}
