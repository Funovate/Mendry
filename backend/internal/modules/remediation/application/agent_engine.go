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
		maxTokens:   4096,
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
	if conversation != nil {
		conversation.RecordModelTurn(req.Continuation, res)
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
		env := &AgentEnvelope{
			SchemaVersion:      EnvelopeVersion,
			Kind:               "requestTool",
			RequestTool:        &requests[0],
			nativeToolRequests: requests,
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
	observer.ModelTurnCompleted(ctx, ModelTurnObservation{
		Run: run, Phase: phase, Sequence: sequence, Duration: time.Since(started),
		Outcome: "success", EnvelopeKind: env.Kind, Request: req, Response: res,
	})

	return env, res, nil
}

const agentSystemPrompt = "You are a diagnosis agent. Reason only over the bounded, " +
	"redacted observations provided. Request read-only tools when you need more " +
	"evidence. Return exactly one JSON envelope per the agent protocol. " +
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
	switch phase {
	case domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext:
		return "Diagnose the incident from available evidence. Classify fixability, " +
			"cite evidence, and either request a read tool or return a diagnosis object. " +
			"If kind is diagnosis, diagnosis must be an object with fixability, confidence, " +
			"causalReasoning, contradictions, missingEvidence, evidenceCitations, and " +
			"recommendedNextAction; never a string. " +
			"For an SSH source, use ssh.inspect: first ls the hinted logPath directory " +
			"and discover actual file names before reading; never assume logPath is a file to tail."
	case domain.RunStatePlanning:
		return "The incident is code-fixable. Produce candidate repair plans with a " +
			"recommended plan, rationale, risk classification, and a suggested unified diff. " +
			"Return kind=planCandidates with a planCandidates object, not a string."
	default:
		return "Process the current remediation phase."
	}
}
