package application

import (
	"encoding/json"
	"errors"
	"strings"

	"mendry/backend/internal/modules/remediation/domain"
)

const (
	protocolCorrectionInvalidEnvelope               = "invalid_envelope"
	protocolCorrectionEvidenceCitation              = "invalid_evidence_citation"
	protocolCorrectionEvidencePath                  = "diagnosis.evidenceCitations[].evidenceRef"
	protocolCorrectionEvidenceField                 = "evidenceId"
	protocolCorrectionEvidenceMessage               = "diagnosis.evidenceCitations entries must use evidenceId as the canonical field. " + diagnosisWireContractInstruction
	protocolCorrectionFixability                    = "invalid_fixability"
	protocolCorrectionFixabilityPath                = "diagnosis.fixability"
	protocolCorrectionFixabilityMessage             = "diagnosis.fixability must be an allowed JSON string, never an object. " + diagnosisWireContractInstruction
	protocolCorrectionConfidence                    = "invalid_confidence"
	protocolCorrectionConfidencePath                = "diagnosis.confidence"
	protocolCorrectionConfidenceMessage             = "diagnosis.confidence must be a JSON number between 0 and 1; do not use low, medium, or high as strings. " + diagnosisWireContractInstruction
	protocolCorrectionSourceCoverage                = "invalid_source_coverage"
	protocolCorrectionSourceCoveragePath            = "diagnosis.sourceCoverage"
	protocolCorrectionSourceCoverageMessage         = "diagnosis.sourceCoverage must be a JSON array, never an object. " + diagnosisWireContractInstruction
	protocolCorrectionTimeAssessment                = "invalid_time_assessment"
	protocolCorrectionTimeAssessmentPath            = "diagnosis.timeAssessment.basis"
	protocolCorrectionTimeAssessmentMessage         = "diagnosis.timeAssessment.basis must be one allowed short enum value, with explanation text kept outside basis. " + diagnosisWireContractInstruction
	protocolCorrectionRequiredDetail                = "required_direct_evidence"
	protocolCorrectionRequiredDetailPath            = "evidence.tencent_cls_detail"
	protocolCorrectionRequiredDetailMessage         = "Tencent CLS webhook detail evidence is mandatory before diagnosis or stop; request evidence.tencent_cls_detail with an empty object and wait for a successful observation"
	protocolCorrectionRequiredStopSuggestion        = "required_stop_suggestion"
	protocolCorrectionRequiredStopSuggestionPath    = "stop.recommendedNextAction"
	protocolCorrectionRequiredStopSuggestionMessage = "stop.recommendedNextAction is required and must be a bounded handoff suggestion for a human operator. " + stopWireContractInstruction
	protocolCorrectionDiagnosisMessage              = "previous response was not a valid agent envelope; diagnosis must be an object. " + diagnosisWireContractInstruction
	protocolCorrectionPlanningInvalidJSON           = "invalid_planning_json"
	protocolCorrectionPlanningSchema                = "invalid_planning_schema_version"
	protocolCorrectionPlanningShape                 = "invalid_planning_envelope"
	protocolCorrectionPlanningKind                  = "invalid_planning_kind"
	protocolCorrectionPlanningCandidates            = "invalid_plan_candidates"
	protocolCorrectionPlanningRecommended           = "invalid_plan_recommended_id"
	protocolCorrectionPlanningDiff                  = "invalid_plan_suggested_diff"
	protocolCorrectionPlanningPlanID                = "invalid_plan_id"
	protocolCorrectionPlanningRisk                  = "invalid_plan_risk"
	protocolCorrectionPlanningRationale             = "invalid_plan_rationale"
	protocolCorrectionPlanningEvidenceRefs          = "invalid_plan_evidence_refs"
	protocolCorrectionPlanningAffectedFiles         = "invalid_plan_affected_files"
	protocolCorrectionPlanningBehavior              = "invalid_plan_intended_behavior"
	protocolCorrectionPlanningRollback              = "invalid_plan_rollback_strategy"
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
		if errors.Is(cause, errStopRecommendationRequired) {
			return requiredStopSuggestionCorrection()
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(cause, &typeErr) && typeErr != nil {
			switch {
			case strings.HasSuffix(typeErr.Field, ".fixability"):
				return ProtocolCorrection{
					Code:          protocolCorrectionFixability,
					Path:          protocolCorrectionFixabilityPath,
					ExpectedField: "string enum",
					Message:       protocolCorrectionFixabilityMessage,
				}
			case strings.HasSuffix(typeErr.Field, ".confidence"):
				return ProtocolCorrection{
					Code:          protocolCorrectionConfidence,
					Path:          protocolCorrectionConfidencePath,
					ExpectedField: "number",
					Message:       protocolCorrectionConfidenceMessage,
				}
			case strings.HasSuffix(typeErr.Field, ".sourceCoverage"):
				return ProtocolCorrection{
					Code:          protocolCorrectionSourceCoverage,
					Path:          protocolCorrectionSourceCoveragePath,
					ExpectedField: "array",
					Message:       protocolCorrectionSourceCoverageMessage,
				}
			case typeErr.Field == "stop.recommendedNextAction" || strings.HasSuffix(typeErr.Field, ".stop.recommendedNextAction"):
				return requiredStopSuggestionCorrection()
			}
		}
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		errors.Is(cause, errInvalidDiagnosisTimeAssessment) {
		return ProtocolCorrection{
			Code:          protocolCorrectionTimeAssessment,
			Path:          protocolCorrectionTimeAssessmentPath,
			ExpectedField: "paired_epoch|explicit_offset|contextual_zone|unresolved",
			Message:       protocolCorrectionTimeAssessmentMessage,
		}
	}

	if phase == domain.RunStatePlanning {
		return planningProtocolCorrection(cause)
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

func planningCorrectionMessage(reason string) string {
	return reason + ". " + planningWireContractInstruction
}

func planningProtocolCorrection(cause error) ProtocolCorrection {
	correction := func(code, path, expected, reason string) ProtocolCorrection {
		return ProtocolCorrection{Code: code, Path: path, ExpectedField: expected, Message: planningCorrectionMessage(reason)}
	}
	if cause == nil {
		return ProtocolCorrection{Code: protocolCorrectionInvalidEnvelope, Message: planningCorrectionMessage("previous response was not a valid planning envelope")}
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(cause, &typeErr) && typeErr != nil {
		switch {
		case strings.HasSuffix(typeErr.Field, ".candidates"):
			return correction(protocolCorrectionPlanningCandidates, "planCandidates.candidates", "non-empty array", "planCandidates.candidates has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".recommendedId"):
			return correction(protocolCorrectionPlanningRecommended, "planCandidates.recommendedId", "candidate planId string", "planCandidates.recommendedId has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".suggestedDiff"):
			return correction(protocolCorrectionPlanningDiff, "planCandidates.suggestedDiff", "non-empty unified diff string", "planCandidates.suggestedDiff has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".planId"):
			return correction(protocolCorrectionPlanningPlanID, "planCandidates.candidates[].planId", "non-empty string", "a candidate planId has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".risk"):
			return correction(protocolCorrectionPlanningRisk, "planCandidates.candidates[].risk", "ordinary|high_risk|denied_control_plane", "a candidate risk has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".rationale"):
			return correction(protocolCorrectionPlanningRationale, "planCandidates.rationale", "non-empty string", "planCandidates.rationale has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".evidenceRefs"):
			return correction(protocolCorrectionPlanningEvidenceRefs, "planCandidates.candidates[].evidenceRefs", "non-empty string array", "a candidate evidenceRefs has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".affectedFiles"):
			return correction(protocolCorrectionPlanningAffectedFiles, "planCandidates.candidates[].affectedFiles", "non-empty string array", "a candidate affectedFiles has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".intendedBehavior"):
			return correction(protocolCorrectionPlanningBehavior, "planCandidates.candidates[].intendedBehavior", "non-empty string", "a candidate intendedBehavior has the wrong JSON type")
		case strings.HasSuffix(typeErr.Field, ".rollbackStrategy"):
			return correction(protocolCorrectionPlanningRollback, "planCandidates.candidates[].rollbackStrategy", "non-empty string", "a candidate rollbackStrategy has the wrong JSON type")
		}
	}

	var syntaxErr *json.SyntaxError
	if errors.As(cause, &syntaxErr) {
		return correction(protocolCorrectionPlanningInvalidJSON, "envelope", "one valid JSON object", "planning response contains invalid JSON syntax")
	}

	safe := cause.Error()
	switch {
	case strings.Contains(safe, "planCandidates: at least one candidate required"):
		return correction(protocolCorrectionPlanningCandidates, "planCandidates.candidates", "non-empty array", "planCandidates.candidates must contain at least one candidate")
	case strings.Contains(safe, "planCandidates: recommendedId is required"):
		return correction(protocolCorrectionPlanningRecommended, "planCandidates.recommendedId", "candidate planId string", "planCandidates.recommendedId is required")
	case strings.Contains(safe, "planCandidates: suggestedDiff is required"):
		return correction(protocolCorrectionPlanningDiff, "planCandidates.suggestedDiff", "non-empty unified diff string", "planCandidates.suggestedDiff is required")
	case strings.Contains(safe, "planCandidates: candidate planId is required"):
		return correction(protocolCorrectionPlanningPlanID, "planCandidates.candidates[].planId", "non-empty string", "every plan candidate requires a planId")
	case strings.Contains(safe, "planCandidates: unknown risk"):
		return correction(protocolCorrectionPlanningRisk, "planCandidates.candidates[].risk", "ordinary|high_risk|denied_control_plane", "candidate risk is not an allowed value")
	case strings.Contains(safe, "planCandidates: rationale is required"):
		return correction(protocolCorrectionPlanningRationale, "planCandidates.rationale", "non-empty string", "planCandidates.rationale is required")
	case strings.Contains(safe, "planCandidates: candidate evidenceRefs is required"):
		return correction(protocolCorrectionPlanningEvidenceRefs, "planCandidates.candidates[].evidenceRefs", "non-empty string array", "every plan candidate requires a non-empty evidenceRefs array")
	case strings.Contains(safe, "planCandidates: candidate affectedFiles is required"):
		return correction(protocolCorrectionPlanningAffectedFiles, "planCandidates.candidates[].affectedFiles", "non-empty string array", "every plan candidate requires a non-empty affectedFiles array")
	case strings.Contains(safe, "planCandidates: candidate intendedBehavior is required"):
		return correction(protocolCorrectionPlanningBehavior, "planCandidates.candidates[].intendedBehavior", "non-empty string", "every plan candidate requires a non-empty intendedBehavior")
	case strings.Contains(safe, "planCandidates: candidate rollbackStrategy is required"):
		return correction(protocolCorrectionPlanningRollback, "planCandidates.candidates[].rollbackStrategy", "non-empty string", "every plan candidate requires a non-empty rollbackStrategy")
	case strings.Contains(safe, "planCandidates: recommendedId") && strings.Contains(safe, "not found in candidates"):
		return correction(protocolCorrectionPlanningRecommended, "planCandidates.recommendedId", "existing candidate planId", "planCandidates.recommendedId must match one candidate planId")
	case strings.Contains(safe, "envelope schema version"):
		return correction(protocolCorrectionPlanningSchema, "schemaVersion", EnvelopeVersion, "planning schemaVersion must be v1")
	case strings.Contains(safe, "is not valid in planning phase"), strings.Contains(safe, "kind field"):
		return correction(protocolCorrectionPlanningKind, "kind", "requestTool|planCandidates", "planning kind must match its single requestTool or planCandidates payload")
	case strings.Contains(safe, "exactly one kind"), strings.Contains(safe, "multiple kinds"):
		return correction(protocolCorrectionPlanningShape, "envelope", "exactly one planning payload", "planning envelope must contain exactly one payload")
	case strings.Contains(safe, "multiple JSON values"):
		return correction(protocolCorrectionPlanningInvalidJSON, "envelope", "one JSON object", "planning response must contain one JSON object only")
	case strings.Contains(safe, "unknown field"):
		return correction(protocolCorrectionPlanningShape, "envelope", "planning wire contract fields only", "planning response contains a field outside the wire contract")
	default:
		return ProtocolCorrection{Code: protocolCorrectionInvalidEnvelope, Message: planningCorrectionMessage("previous response was not a recognized planning envelope")}
	}
}

func isTrustedPlanningCorrection(correction ProtocolCorrection) bool {
	allowedMessages := map[string][]string{
		protocolCorrectionPlanningInvalidJSON: {
			"planning response contains invalid JSON syntax",
			"planning response must contain one JSON object only",
		},
		protocolCorrectionPlanningSchema:        {"planning schemaVersion must be v1"},
		protocolCorrectionPlanningShape:         {"planning envelope must contain exactly one payload", "planning response contains a field outside the wire contract"},
		protocolCorrectionPlanningKind:          {"planning kind must match its single requestTool or planCandidates payload"},
		protocolCorrectionPlanningCandidates:    {"planCandidates.candidates has the wrong JSON type", "planCandidates.candidates must contain at least one candidate"},
		protocolCorrectionPlanningRecommended:   {"planCandidates.recommendedId has the wrong JSON type", "planCandidates.recommendedId is required", "planCandidates.recommendedId must match one candidate planId"},
		protocolCorrectionPlanningDiff:          {"planCandidates.suggestedDiff has the wrong JSON type", "planCandidates.suggestedDiff is required"},
		protocolCorrectionPlanningPlanID:        {"a candidate planId has the wrong JSON type", "every plan candidate requires a planId"},
		protocolCorrectionPlanningRisk:          {"a candidate risk has the wrong JSON type", "candidate risk is not an allowed value"},
		protocolCorrectionPlanningRationale:     {"planCandidates.rationale has the wrong JSON type", "planCandidates.rationale is required"},
		protocolCorrectionPlanningEvidenceRefs:  {"a candidate evidenceRefs has the wrong JSON type", "every plan candidate requires a non-empty evidenceRefs array"},
		protocolCorrectionPlanningAffectedFiles: {"a candidate affectedFiles has the wrong JSON type", "every plan candidate requires a non-empty affectedFiles array"},
		protocolCorrectionPlanningBehavior:      {"a candidate intendedBehavior has the wrong JSON type", "every plan candidate requires a non-empty intendedBehavior"},
		protocolCorrectionPlanningRollback:      {"a candidate rollbackStrategy has the wrong JSON type", "every plan candidate requires a non-empty rollbackStrategy"},
	}
	for _, reason := range allowedMessages[correction.Code] {
		if correction.Message == planningCorrectionMessage(reason) {
			return true
		}
	}
	return false
}

func requiredTencentDetailCorrection() ProtocolCorrection {
	return ProtocolCorrection{
		Code:          protocolCorrectionRequiredDetail,
		Path:          protocolCorrectionRequiredDetailPath,
		ExpectedField: "successful tool observation",
		Message:       protocolCorrectionRequiredDetailMessage,
	}
}

func requiredStopSuggestionCorrection() ProtocolCorrection {
	return ProtocolCorrection{
		Code:          protocolCorrectionRequiredStopSuggestion,
		Path:          protocolCorrectionRequiredStopSuggestionPath,
		ExpectedField: "non-empty string",
		Message:       protocolCorrectionRequiredStopSuggestionMessage,
	}
}

func genericProtocolCorrection(phase domain.RunState) ProtocolCorrection {
	if phase == domain.RunStatePlanning {
		return planningProtocolCorrection(nil)
	}
	return ProtocolCorrection{Code: protocolCorrectionInvalidEnvelope, Message: protocolCorrectionDiagnosisMessage}
}

func normalizeProtocolCorrection(phase domain.RunState, correction ProtocolCorrection) ProtocolCorrection {
	if phase == domain.RunStatePlanning {
		if isTrustedPlanningCorrection(correction) {
			return correction
		}
		return genericProtocolCorrection(phase)
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionRequiredDetail {
		return requiredTencentDetailCorrection()
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionRequiredStopSuggestion {
		return requiredStopSuggestionCorrection()
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionFixability {
		return ProtocolCorrection{
			Code:          protocolCorrectionFixability,
			Path:          protocolCorrectionFixabilityPath,
			ExpectedField: "string enum",
			Message:       protocolCorrectionFixabilityMessage,
		}
	}
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
		correction.Code == protocolCorrectionSourceCoverage {
		return ProtocolCorrection{
			Code:          protocolCorrectionSourceCoverage,
			Path:          protocolCorrectionSourceCoveragePath,
			ExpectedField: "array",
			Message:       protocolCorrectionSourceCoverageMessage,
		}
	}
	if (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) &&
		correction.Code == protocolCorrectionTimeAssessment {
		return ProtocolCorrection{
			Code:          protocolCorrectionTimeAssessment,
			Path:          protocolCorrectionTimeAssessmentPath,
			ExpectedField: "paired_epoch|explicit_offset|contextual_zone|unresolved",
			Message:       protocolCorrectionTimeAssessmentMessage,
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
