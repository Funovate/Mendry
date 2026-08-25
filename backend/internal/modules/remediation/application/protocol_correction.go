package application

import (
	"encoding/json"
	"errors"
	"strings"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	protocolCorrectionInvalidEnvelope   = "invalid_envelope"
	protocolCorrectionEvidenceCitation  = "invalid_evidence_citation"
	protocolCorrectionEvidencePath      = "diagnosis.evidenceCitations[].evidenceRef"
	protocolCorrectionEvidenceField     = "evidenceId"
	protocolCorrectionEvidenceMessage   = "diagnosis.evidenceCitations entries must use evidenceId as the canonical field; each citation is either a persisted evidence ID string or an object with evidenceId and optional classification"
	protocolCorrectionConfidence        = "invalid_confidence"
	protocolCorrectionConfidencePath    = "diagnosis.confidence"
	protocolCorrectionConfidenceMessage = "diagnosis.confidence must be a JSON number between 0 and 1, for example 0.2; do not use low, medium, or high as strings"
	protocolCorrectionDiagnosisMessage  = "previous response was not a valid agent envelope; if kind is diagnosis, diagnosis must be an object with fixability, and each evidenceCitations entry must be either a persisted evidence ID string or an object with evidenceId and optional classification"
	protocolCorrectionPlanningMessage   = "previous response was not a valid planCandidates envelope; return schemaVersion=v1, kind=planCandidates, and a planCandidates object"
)

// ProtocolCorrection 是一次被拒绝模型信封的有界 provider-neutral 修正；进入
// conversation context 前必须按 allowlist 归一化。
type ProtocolCorrection struct {
	Code          string
	Path          string
	ExpectedField string
	Message       string
}

// ProtocolCorrectionFor 只把已知 typed validation failure 映射为具体指引，未知
// decoder 或 validation error 使用按 phase 选择的安全 fallback。
func ProtocolCorrectionFor(phase domain.RunState, cause error) ProtocolCorrection {
	if phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext {
		var typeErr *json.UnmarshalTypeError
		if errors.As(cause, &typeErr) && typeErr != nil && strings.HasSuffix(typeErr.Field, ".confidence") {
			return ProtocolCorrection{
				Code:          protocolCorrectionConfidence,
				Path:          protocolCorrectionConfidencePath,
				ExpectedField: "number",
				Message:       protocolCorrectionConfidenceMessage,
			}
		}
	}

	var fieldErr *domain.EvidenceCitationFieldError
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		errors.As(cause, &fieldErr) && fieldErr != nil && fieldErr.Field == "evidenceRef" {
		return ProtocolCorrection{
			Code:          protocolCorrectionEvidenceCitation,
			Path:          protocolCorrectionEvidencePath,
			ExpectedField: protocolCorrectionEvidenceField,
			Message:       protocolCorrectionEvidenceMessage,
		}
	}
	return genericProtocolCorrection(phase)
}

func genericProtocolCorrection(phase domain.RunState) ProtocolCorrection {
	if phase == domain.RunStatePlanning {
		return ProtocolCorrection{Code: protocolCorrectionInvalidEnvelope, Message: protocolCorrectionPlanningMessage}
	}
	return ProtocolCorrection{Code: protocolCorrectionInvalidEnvelope, Message: protocolCorrectionDiagnosisMessage}
}

func normalizeProtocolCorrection(phase domain.RunState, correction ProtocolCorrection) ProtocolCorrection {
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionConfidence {
		return ProtocolCorrection{
			Code:          protocolCorrectionConfidence,
			Path:          protocolCorrectionConfidencePath,
			ExpectedField: "number",
			Message:       protocolCorrectionConfidenceMessage,
		}
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionEvidenceCitation {
		return ProtocolCorrection{
			Code:          protocolCorrectionEvidenceCitation,
			Path:          protocolCorrectionEvidencePath,
			ExpectedField: protocolCorrectionEvidenceField,
			Message:       protocolCorrectionEvidenceMessage,
		}
	}
	return genericProtocolCorrection(phase)
}
