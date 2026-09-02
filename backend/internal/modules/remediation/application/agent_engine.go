package application

import (
	"context"
	"fmt"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// AgentEngine runs one bounded model turn: it builds the phase prompt and tool
// schemas, calls the LLM port, and returns a strictly validated envelope plus
// normalized usage. It receives no adapter clients and no credentials — only
// the LLM port and the tool gateway (for advertised tool schemas). Tool
// execution is never performed here; that is the coordinator's job via the
// gateway.
type AgentEngine struct {
	llmPort     domain.LLMProviderPort
	toolGateway *ToolGateway
	maxTokens   int
}

// NewAgentEngine creates a new agent engine.
func NewAgentEngine(
	llmPort domain.LLMProviderPort,
	toolGateway *ToolGateway,
) *AgentEngine {
	return &AgentEngine{
		llmPort:     llmPort,
		toolGateway: toolGateway,
		maxTokens:   8192,
	}
}

// Turn runs a single model turn for the given phase over the supplied bounded
// context, returning the validated envelope and normalized usage.
func (e *AgentEngine) Turn(
	ctx context.Context,
	phase domain.RunState,
	projectID string,
	contextText string,
) (*AgentEnvelope, domain.ModelResult, error) {
	return e.TurnObserved(ctx, RunIdentity{}, noopRunObserver{}, 0, phase, projectID, contextText)
}

// TurnObserved 执行模型轮次并在解析完成后发出关联 observation。
func (e *AgentEngine) TurnObserved(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	sequence int64,
	phase domain.RunState,
	projectID string,
	contextText string,
) (*AgentEnvelope, domain.ModelResult, error) {
	return e.TurnObservedWithConversation(ctx, run, observer, sequence, phase, projectID, contextText, nil)
}

// TurnObservedWithConversation 执行一轮模型调用，并把 provider-native 消息
// 历史和本轮 assistant 结果写回有界 conversation。
func (e *AgentEngine) TurnObservedWithConversation(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	sequence int64,
	phase domain.RunState,
	projectID string,
	contextText string,
	conversation *AgentConversation,
) (*AgentEnvelope, domain.ModelResult, error) {
	return e.TurnObservedWithConversationAndTools(
		ctx, run, observer, sequence, phase, projectID, contextText,
		e.toolGateway.AdvertisedToolDefinitions(phase), conversation,
	)
}

// TurnObservedWithConversationAndTools 执行一轮模型调用，并使用本次 run
// 已解析的 capability catalog；调用方可据 source metadata 收紧广告清单。
func (e *AgentEngine) TurnObservedWithConversationAndTools(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	sequence int64,
	phase domain.RunState,
	projectID string,
	contextText string,
	tools []domain.ToolDefinition,
	conversation *AgentConversation,
) (*AgentEnvelope, domain.ModelResult, error) {
	observer = normalizeRunObserver(observer)
	phasePrompt := e.buildPrompt(phase)
	userMessage := phasePrompt + "\n\n## Context\n" + contextText
	req := domain.ModelTurn{
		ProjectID:    projectID,
		SystemPrompt: agentSystemPrompt,
		UserMessage:  userMessage,
		Tools:        append([]domain.ToolDefinition(nil), tools...),
		MaxTokens:    e.maxTokens,
		Temperature:  0,
	}
	if conversation != nil {
		req.Messages = conversation.History()
		req.Continuation = conversation.NativeContinuation(phasePrompt)
	}

	started := time.Now()
	res, err := e.llmPort.Complete(ctx, req)
	if err != nil {
		turnErr := fmt.Errorf("model turn: %w", err)
		observer.ModelTurnCompleted(ctx, ModelTurnObservation{
			Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
			Outcome: "failure", FailureClass: "provider", ErrorMessage: turnErr.Error(), Request: req, Response: res,
		})
		return nil, res, turnErr
	}
	if len(res.ToolCalls) > 0 {
		if res.Content != "" {
			protocolErr := wrapEnvelopeError("model returned tool calls and envelope content together", nil)
			observer.ModelTurnCompleted(ctx, ModelTurnObservation{
				Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
				Outcome: "failure", FailureClass: "protocol", ErrorMessage: protocolErr.Error(), Request: req, Response: res,
			})
			return nil, res, protocolErr
		}
		requests := make([]RequestTool, 0, len(res.ToolCalls))
		for _, call := range res.ToolCalls {
			request := RequestTool{ToolName: call.Name, Parameters: call.Arguments, CallID: call.ID}
			if err := validateRequestTool(&request); err != nil {
				validationErr := wrapEnvelopeError("validate native tool call", err)
				observer.ModelTurnCompleted(ctx, ModelTurnObservation{
					Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
					Outcome: "failure", FailureClass: "protocol", ErrorMessage: validationErr.Error(), Request: req, Response: res,
				})
				return nil, res, validationErr
			}
			requests = append(requests, request)
		}
		if err := validateEnvelopeForPhase(phase, "requestTool"); err != nil {
			protocolErr := wrapEnvelopeError("validate agent envelope phase", err)
			observer.ModelTurnCompleted(ctx, ModelTurnObservation{
				Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
				Outcome: "failure", FailureClass: "protocol", ErrorMessage: protocolErr.Error(), Request: req, Response: res,
			})
			return nil, res, protocolErr
		}
		env := &AgentEnvelope{
			SchemaVersion:      EnvelopeVersion,
			Kind:               "requestTool",
			RequestTool:        &requests[0],
			nativeToolRequests: requests,
		}
		if conversation != nil {
			conversation.RecordModelTurn(req.Continuation, res)
		}
		observer.ModelTurnCompleted(ctx, ModelTurnObservation{
			Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
			Outcome: "success", EnvelopeKind: env.Kind, Request: req, Response: res,
		})
		return env, res, nil
	}

	env, err := DecodeAgentEnvelope([]byte(res.Content))
	if err != nil {
		decodeErr := wrapEnvelopeError("decode agent envelope", err)
		observer.ModelTurnCompleted(ctx, ModelTurnObservation{
			Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
			Outcome: "failure", FailureClass: "decode", ErrorMessage: decodeErr.Error(), Request: req, Response: res,
		})
		return nil, res, decodeErr
	}
	if err := validateEnvelopeForPhase(phase, env.Kind); err != nil {
		protocolErr := wrapEnvelopeError("validate agent envelope phase", err)
		observer.ModelTurnCompleted(ctx, ModelTurnObservation{
			Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
			Outcome: "failure", FailureClass: "protocol", ErrorMessage: protocolErr.Error(), Request: req, Response: res,
		})
		return nil, res, protocolErr
	}
	if conversation != nil {
		conversation.RecordModelTurn(req.Continuation, res)
	}
	observer.ModelTurnCompleted(ctx, ModelTurnObservation{
		Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
		Outcome: "success", EnvelopeKind: env.Kind, Request: req, Response: res,
	})

	return env, res, nil
}

func validateEnvelopeForPhase(phase domain.RunState, kind string) error {
	valid := false
	switch phase {
	case domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext:
		valid = kind == "requestTool" || kind == "diagnosis" || kind == "stop" || kind == "exhaustion"
	case domain.RunStatePlanning:
		valid = kind == "requestTool" || kind == "planCandidates"
	case domain.RunStatePatching:
		valid = kind == "requestTool" || kind == "patchComplete" || kind == "stop"
	case domain.RunStateValidating:
		valid = kind == "requestTool" || kind == "validationAssessment" || kind == "stop"
	}
	if valid {
		return nil
	}
	return fmt.Errorf("envelope kind %q is not valid in %s phase", kind, phase)
}

const diagnosisConfidenceInstruction = "confidence must be a JSON number between 0 and 1 (for example 0.2), never a label string such as low, medium, or high"

const stopWireContractInstruction = `Stop wire contract: if you must stop, return exactly {"schemaVersion":"v1","kind":"stop","stop":{"reason":"...","recommendedNextAction":"..."}}. stop.recommendedNextAction is required, must be a non-empty bounded handoff suggestion for a human operator, and should state the next safe action or evidence needed. Do not stop with an empty or omitted recommendation.`

const diagnosisWireContractInstruction = `Diagnosis wire contract: return {"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"...","contradictions":[],"missingEvidence":[],"evidenceCitations":[],"recommendedNextAction":"...","alertQuality":"enriched","sourceCoverage":[],"timeAssessment":{"originalValues":[],"normalizedStart":"","normalizedEnd":"","basis":"unresolved","certainty":"unresolved","contradictory":false},"correlation":{"temporal":false,"operational":false,"hostIdentity":false,"directBridge":false},"causalClosure":{"explainsOriginalSymptom":false,"explanation":"..."},"materialContradictions":[],"testSuspected":false,"testPolicyMatched":false,"hypotheses":[]}}. fixability is a JSON string, never an object, and must be one of code_fixable, external_dependency, configuration, data, infrastructure, insufficient_evidence, unsafe_to_automate. confidence is a number from 0 to 1. contradictions, missingEvidence, materialContradictions, and hypotheses are arrays. evidenceCitations is an array whose entries are either a persisted evidence ID string or {"evidenceId":"...","classification":"direct_fault"} with optional classification; classification, when present, must be one of direct_fault, correlated_supporting, contextual, unrelated, contradictory. alertQuality is sparse, anchor_only, or enriched. sourceCoverage is a JSON array, never an object, of {"sourceId":"...","kind":"...","primary":true,"status":"inspected_success","reason":"...","directBridge":false}; status must be one of configured, inspected_success, inspected_empty, unavailable, not_applicable, not_inspected. timeAssessment.basis must be exactly one of paired_epoch, explicit_offset, contextual_zone, unresolved; never put explanation text in basis. Keep timeAssessment.certainty a short label such as high, low, or unresolved. timeAssessment, correlation, and causalClosure otherwise use the object shapes shown. Each hypothesis is {"id":"...","summary":"...","evidenceRefs":[],"nonActionable":true}.`

const planningWireContractInstruction = `Planning wire contract: you may first call any advertised read-only repository tools when you need code, dependency, or impact context. After tool observations are sufficient, return exactly {"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"plan-1","evidenceRefs":["evidence-id"],"affectedFiles":["path/to/file.go"],"intendedBehavior":"...","risk":"ordinary","rollbackStrategy":"..."}],"recommendedId":"plan-1","rationale":"...","suggestedDiff":"diff --git a/path/to/file.go b/path/to/file.go\\n..."}}. candidates must be a non-empty array. Every candidate requires non-empty planId, evidenceRefs and affectedFiles arrays, intendedBehavior, risk, and rollbackStrategy. risk must be exactly one of ordinary, high_risk, denied_control_plane. recommendedId is required and must equal one candidate planId. rationale and suggestedDiff are required strings, and suggestedDiff must be a non-empty unified diff. Return one envelope only; do not return diagnosis or stop in planning.`

const agentSystemPrompt = "You are a remediation agent. Reason only over the bounded " +
	"operational evidence and credential-isolated metadata provided. Evidence may " +
	"contain complete original log/provider fields; do not treat absent fields as " +
	"proof, and never invent evidence or authority-bearing URLs, credentials, or " +
	"control material. Request only policy-advertised bounded tools when more " +
	"Apply this global time policy for every provider: compare paired epoch values " +
	"first, then explicit timestamp offsets, then source/system time-zone context " +
	"visible in the evidence; otherwise mark time unresolved and low-certainty. " +
	"Preserve original time strings and cite the normalized comparison and uncertainty. " +
	diagnosisConfidenceInstruction + ". " +
	"Return exactly one JSON envelope per the agent protocol. " +
	"kind=diagnosis requires a diagnosis object, never a string."

// wrapEnvelopeError 把可纠正的信封/协议错误标成 ErrInvalidEnvelope，
// 让 coordinator 回喂模型而不是把 run 直接打成 failed。
func wrapEnvelopeError(op string, err error) error {
	if err == nil {
		return fmt.Errorf("%s: %w", op, ErrInvalidEnvelope)
	}
	return fmt.Errorf("%s: %w: %w", op, ErrInvalidEnvelope, err)
}

// buildPrompt constructs the phase-specific instruction.
func (e *AgentEngine) buildPrompt(phase domain.RunState) string {
	const toolRecoveryInstruction = "When a tool observation reports recoveryAction, follow it exactly: " +
		"correct_request means inspect the sanitized parameters and tool schema, then issue one corrected request instead of replaying unchanged input; " +
		"retry_transient means retry only while retriesRemaining is greater than zero; " +
		"use_fallback or retriesRemaining zero means do not replay that tool/error failure. " +
		"Use another bounded evidence tool or preserve the gap as missing evidence, and never make an actionable diagnosis depend on a failed, unpersisted tool result. "
	switch phase {
	case domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext:
		return "Diagnose the incident from available evidence. Objective: establish whether " +
			"the original alert symptom has an evidence-backed causal explanation, classify " +
			"fixability, and cite persisted evidence with its stored classification. Completion " +
			"criteria: return one diagnosis envelope that satisfies the wire contract below and " +
			"the service fact gate; or, when no material evidence remains collectible, a non-code " +
			"or insufficient-evidence classification with a safe recommended next action. " +
			"You may request any advertised bounded read tool in any useful order; there is no " +
			"required first tool and no mandated sequence. Available capability classes: " +
			"repository (list/read/search/history over the exact deployed code), " +
			"provider_evidence (trusted provider detail and persisted evidence by ID), " +
			"runtime_logs (Docker logs / evidence search and context), ssh_inspect (read-only " +
			"host inspection). A stack, source path, function, or line in trusted provider " +
			"detail is a strong code-localization hint, not a rule that skips runtime " +
			"correlation: inspect the exact deployed source first, later, or between refined " +
			"runtime queries as the causal question requires; runtime correlation is likewise " +
			"not mandatory for every code fix. " +
			diagnosisWireContractInstruction + " " +
			"Include alertQuality, sourceCoverage, timeAssessment, correlation, causalClosure, " +
			"causalReasoning, contradictions, materialContradictions, missingEvidence, " +
			"evidenceCitations, recommendedNextAction, and any non-actionable hypotheses. " +
			"timeAssessment must preserve originalValues, normalized " +
			"instants/ranges, basis, certainty, and contradictory status. correlation " +
			"must state temporal, operational, host identity, and direct bridge status. " +
			"causalClosure must explicitly say whether the original alert symptom is " +
			"explained and why. A test-like title is only testSuspected unless an " +
			"auditable structured test policy matches; continue investigating real fault " +
			"evidence. Test authorization is a separate audit dimension: testPolicyMatched=false " +
			"or unknown does not by itself make root-cause evidence insufficient. When runtime, " +
			"request, and deployed-code evidence explains the original symptom, keep causalClosure " +
			"true, classify the actual fixability, and record the unmatched test policy as an audit " +
			"finding or recommended next action. Use insufficient_evidence only when missing material " +
			"evidence prevents causal explanation or a safe fixability classification. Missing direct fault " +
			"evidence remains a fact-gate issue. Unresolved time, host identity, request correlation, or source " +
			"coverage must be recorded with whether the gap is material to this causal chain; none is a universal " +
			"prerequisite for every code fix. " +
			"Tool guidance (no ordering is required): for an SSH source, ssh.inspect may list " +
			"the hinted logPath directory first to discover actual file names; never assume " +
			"logPath is a file to tail. " +
			"When trusted Tencent CLS detail contains an error, stack, source path, or line number, " +
			"treat it as an anchor and use repository inspection and runtime collection in any " +
			"order that closes causality. AnalysisOriginal.time in the detail is the " +
			"UTC log-event time; use it directly as the since/until anchor with a narrow window around " +
			"that time. docker.logs returns only the tail of the requested window, so inspect " +
			"window_lines, returned_lines, filtered, truncated, coverage_limited, and refinement_required " +
			"before deciding that no failure is present. window_lines=-1 means the connector deliberately " +
			"skipped an additional full-log count. When refinement_required is true, narrow the window or add " +
			"a pattern before returning a terminal diagnosis. For a panic or stack anchor, request pattern with context_after to capture the " +
			"following goroutine frames. Map any provider path to a repository-relative path and " +
			"use repository.read_file plus repository.search for the stable fault, message, or " +
			"function. " + toolRecoveryInstruction + "Do not repeatedly retry " +
			"a non-retryable detail failure; use available fallback tools and report the failure as " +
			"missing evidence. " +
			stopWireContractInstruction
	case domain.RunStatePlanning:
		return "The incident is code-fixable. Inspect the advertised repository tools whenever more code, dependency, or impact context is needed, then produce candidate repair plans with a recommended plan, rationale, risk classification, and a suggested unified diff. " + toolRecoveryInstruction + planningWireContractInstruction
	case domain.RunStateValidating:
		return "Validate the current workspace patch using only the advertised approved command IDs. Inspect bounded workspace status or files when the result is ambiguous, then return one validationAssessment envelope. The service trusts the actual validation runner result, not a model claim. A failed validation should explain the bounded repair direction; it does not authorize publication. " + toolRecoveryInstruction
	case domain.RunStatePatching:
		return "Apply the selected repair plan in the isolated workspace based on the exact deployed baseline. Use only the advertised bounded workspace tools, inspect status/files before changing content when needed, and apply small idempotent patches with the supplied tree precondition. After a patch is applied, return one patchComplete envelope with a bounded summary. Never access Git credentials, host paths, arbitrary commands, or SCM publication. " + toolRecoveryInstruction
	default:
		return "Process the current remediation phase."
	}
}
