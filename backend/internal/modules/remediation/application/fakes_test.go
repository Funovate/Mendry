package application_test

import (
	"context"
	"fmt"
	"time"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// fakeRunStore is an in-memory RunStore that records every mutation and
// enforces the from-state guard, mirroring the optimistic-version contract.
type fakeRunStore struct {
	state             domain.RunState
	created           *domain.Run
	decisions         []domain.Decision
	submitted         []domain.SubmittedDiagnosis
	plans             []domain.RepairPlanCandidate
	recommendedID     string
	suggestedDiff     string
	notifications     []application.TerminalNotification
	invocations       []domain.ToolInvocation
	transitions       []stateEdge
	effects           []domain.Effect
	budget            domain.BudgetCounters
	provider          string
	modelName         string
	createErr         error
	invocationErr     error
	submittedErr      error
	latestDecisionErr error
	// mode 是 CreateSeriesAndRun 返回 run 时快照的 agent loop 模式；空值保持
	// legacy，供 resilient_v1 测试注入。
	mode domain.AgentLoopMode
}

type stateEdge struct {
	from domain.RunState
	to   domain.RunState
}

func newFakeRunStore() *fakeRunStore {
	return &fakeRunStore{state: domain.RunStateQueued}
}

func (f *fakeRunStore) CreateSeriesAndRun(_ context.Context, in domain.NewRun) (domain.Run, error) {
	if f.createErr != nil {
		return domain.Run{}, f.createErr
	}
	if f.created != nil && f.created.IncidentID == in.IncidentID &&
		f.created.LifecycleGeneration == in.LifecycleGeneration && f.created.DeployedCommit == in.DeployedCommit {
		return *f.created, nil
	}
	f.state = domain.RunStateQueued
	run := domain.Run{
		RunID:               "run-1",
		SeriesID:            "series-1",
		IncidentID:          in.IncidentID,
		LifecycleGeneration: in.LifecycleGeneration,
		DeployedCommit:      in.DeployedCommit,
		AttemptNumber:       1,
		State:               domain.RunStateQueued,
		Origin:              in.TriggerReason,
		TriggerReason:       in.TriggerReason,
		ContextVersion:      in.ContextVersion,
		Version:             1,
		AgentLoopMode:       f.mode,
	}
	f.created = &run
	return run, nil
}

func (f *fakeRunStore) AppendDecision(_ context.Context, _ string, d domain.Decision) error {
	f.decisions = append(f.decisions, d)
	return nil
}

// SubmittedDiagnosisStore 审计 companion：记录追加的 submitted 行，并提供
// 与 postgres RunStore 一致的 LatestDecisionID 语义（冻结的 AppendDecision
// 不返回 ID，审计链用最近一次 decision 解析 submitted→accepted join）。
func (f *fakeRunStore) AppendSubmittedDiagnosis(_ context.Context, _ string, d domain.SubmittedDiagnosis) error {
	if f.submittedErr != nil {
		return f.submittedErr
	}
	f.submitted = append(f.submitted, d)
	return nil
}

func (f *fakeRunStore) ListSubmittedDiagnoses(_ context.Context, _ string) ([]domain.SubmittedDiagnosis, error) {
	return append([]domain.SubmittedDiagnosis(nil), f.submitted...), nil
}

func (f *fakeRunStore) LatestDecisionID(_ context.Context, _ string) (string, error) {
	if f.latestDecisionErr != nil {
		return "", f.latestDecisionErr
	}
	if len(f.decisions) == 0 {
		return "", nil
	}
	return fmt.Sprintf("decision-%d", len(f.decisions)), nil
}

func (f *fakeRunStore) RecordToolInvocation(_ context.Context, _ string, t domain.ToolInvocation) error {
	if f.invocationErr != nil {
		return f.invocationErr
	}
	f.invocations = append(f.invocations, t)
	return nil
}

func (f *fakeRunStore) Transition(_ context.Context, _ string, from, to domain.RunState, effect domain.Effect) error {
	if f.state != from {
		return fmt.Errorf("state mismatch: have %s, want from %s", f.state, from)
	}
	f.state = to
	if f.created != nil {
		f.created.State = to
		// 镜像 Postgres RunStore：state update 推进一次 version，带 counters 的
		// effect 还会由 IncrementRunCounters 再推进一次。
		f.created.Version++
		if fakeHasCounterEffect(effect) {
			f.created.Version++
		}
	}
	f.transitions = append(f.transitions, stateEdge{from, to})
	f.effects = append(f.effects, effect)
	f.budget.ModelCalls += int64(effect.ModelCalls)
	f.budget.ModelTokens += effect.ModelTokensIn + effect.ModelTokensOut
	f.budget.ModelCostCents += effect.ModelCostCents
	if effect.ModelProvider != "" {
		f.provider = effect.ModelProvider
	}
	if effect.ModelName != "" {
		f.modelName = effect.ModelName
	}
	f.budget.ToolCalls += int64(effect.ToolCalls)
	f.budget.EvidenceBytes += effect.EvidenceBytes
	f.budget.RepositoryBytes += effect.RepositoryBytes
	return nil
}

func fakeHasCounterEffect(effect domain.Effect) bool {
	return effect.ModelCalls > 0 || effect.ModelTokensIn > 0 || effect.ModelTokensOut > 0 ||
		effect.ModelCostCents > 0 || effect.ModelProvider != "" || effect.ModelName != "" ||
		effect.ToolCalls > 0 || effect.EvidenceBytes > 0 || effect.RepositoryBytes > 0
}

func (f *fakeRunStore) Get(_ context.Context, runID string) (domain.RunAggregate, error) {
	run := domain.Run{RunID: runID, State: f.state, Budget: f.budget, ModelProvider: f.provider, ModelName: f.modelName}
	if f.created != nil {
		run.ProjectID = f.created.ProjectID
		run.IncidentID = f.created.IncidentID
		run.LifecycleGeneration = f.created.LifecycleGeneration
		run.DeployedCommit = f.created.DeployedCommit
		run.SeriesID = f.created.SeriesID
		run.AttemptNumber = f.created.AttemptNumber
		run.Version = f.created.Version
		run.AgentLoopMode = f.created.AgentLoopMode
		run.AgentLoopPolicyVersion = f.created.AgentLoopPolicyVersion
	}
	return domain.RunAggregate{
		Run:               run,
		Decisions:         f.decisions,
		Plans:             f.plans,
		ToolInvocations:   f.invocations,
		SuggestedDiff:     f.suggestedDiff,
		RecommendedPlanID: f.recommendedID,
	}, nil
}

func (f *fakeRunStore) AppendPlans(_ context.Context, _ string, plans []domain.RepairPlanCandidate, recommendedID string) error {
	f.plans = append([]domain.RepairPlanCandidate(nil), plans...)
	f.recommendedID = recommendedID
	return nil
}

func (f *fakeRunStore) RecordSuggestedDiff(_ context.Context, _ string, diff string) error {
	f.suggestedDiff = diff
	return nil
}

func (f *fakeRunStore) GetLatestForIncident(_ context.Context, incidentID string, generation int64, deployedCommit string) (domain.RunAggregate, error) {
	if f.created == nil || f.created.IncidentID != incidentID ||
		f.created.LifecycleGeneration != generation || f.created.DeployedCommit != deployedCommit {
		return domain.RunAggregate{}, application.ErrNotFound
	}
	return f.Get(context.Background(), f.created.RunID)
}

func (f *fakeRunStore) GetLatestPlanningCheckpoint(_ context.Context, seriesID string, contextVersion int64, throughAttemptNumber int32) (domain.RunAggregate, error) {
	if f.created == nil || f.created.SeriesID != seriesID || f.created.ContextVersion != contextVersion ||
		f.created.AttemptNumber > throughAttemptNumber || len(f.decisions) == 0 ||
		f.decisions[len(f.decisions)-1].Fixability != domain.FixabilityCodeFixable {
		return domain.RunAggregate{}, domain.ErrPlanningCheckpointNotFound
	}
	return f.Get(context.Background(), f.created.RunID)
}

func (f *fakeRunStore) Notify(_ context.Context, n application.TerminalNotification) error {
	f.notifications = append(f.notifications, n)
	return nil
}

func (f *fakeRunStore) countTransitionsTo(to domain.RunState) int {
	n := 0
	for _, e := range f.transitions {
		if e.from != e.to && e.to == to {
			n++
		}
	}
	return n
}

// fakeRepoPort is a canned RepositoryReadPort that counts adapter calls so
// tests can prove the gateway rejects before any adapter is invoked.
type fakeRepoPort struct {
	calls   int
	lastRef domain.RepoRef
}

func (f *fakeRepoPort) ListTree(_ context.Context, ref domain.RepoRef, _ string, _ domain.TreeOptions) (domain.TreeListing, error) {
	f.calls++
	f.lastRef = ref
	return domain.TreeListing{Entries: []domain.TreeEntry{{Path: "main.go", Type: "file"}}}, nil
}

func (f *fakeRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{Path: path, Content: []byte("package main")}, nil
}

func (f *fakeRepoPort) Search(_ context.Context, ref domain.RepoRef, _ domain.SearchQuery) (domain.SearchResult, error) {
	f.calls++
	f.lastRef = ref
	return domain.SearchResult{}, nil
}

func (f *fakeRepoPort) History(_ context.Context, ref domain.RepoRef, _ string, _ domain.HistoryOptions) (domain.History, error) {
	f.calls++
	f.lastRef = ref
	return domain.History{}, nil
}

type delayedRepoPort struct {
	fakeRepoPort
	delay time.Duration
}

type failingReadRepoPort struct {
	fakeRepoPort
	err error
}

func (f *failingReadRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{Path: path}, f.err
}

type failingListRepoPort struct {
	fakeRepoPort
	err error
}

func (f *failingListRepoPort) ListTree(_ context.Context, ref domain.RepoRef, _ string, _ domain.TreeOptions) (domain.TreeListing, error) {
	f.calls++
	f.lastRef = ref
	return domain.TreeListing{}, f.err
}

func (f *delayedRepoPort) ListTree(ctx context.Context, ref domain.RepoRef, path string, opts domain.TreeOptions) (domain.TreeListing, error) {
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		f.calls++
		f.lastRef = ref
		return domain.TreeListing{}, ctx.Err()
	case <-timer.C:
		return f.fakeRepoPort.ListTree(ctx, ref, path, opts)
	}
}

// fakeEvidencePort is a canned EvidenceLogPort that counts adapter calls.
type fakeEvidencePort struct {
	calls     int
	lastScope domain.EvidenceScope
}

type bootstrapEvidenceLoader struct {
	value      domain.BootstrapEvidence
	incidentID string
	calls      int
}

func (l *bootstrapEvidenceLoader) LoadBootstrapEvidence(_ context.Context, incidentID string) (domain.BootstrapEvidence, error) {
	l.calls++
	l.incidentID = incidentID
	return l.value, nil
}

type failingSearchEvidencePort struct {
	fakeEvidencePort
	err error
}

func (f *failingSearchEvidencePort) Search(_ context.Context, scope domain.EvidenceScope, _ domain.LogQuery) (domain.EvidencePage, error) {
	f.calls++
	f.lastScope = scope
	return domain.EvidencePage{}, f.err
}

func (f *fakeEvidencePort) Search(_ context.Context, scope domain.EvidenceScope, _ domain.LogQuery) (domain.EvidencePage, error) {
	f.calls++
	f.lastScope = scope
	return domain.EvidencePage{Lines: []domain.EvidenceLine{{EvidenceID: "ev-1", Message: "boom"}}}, nil
}

func (f *fakeEvidencePort) GetContext(_ context.Context, scope domain.EvidenceScope, anchor domain.EvidenceAnchor) (domain.EvidencePage, error) {
	f.calls++
	f.lastScope = scope
	return domain.EvidencePage{Lines: []domain.EvidenceLine{{EvidenceID: anchor.EvidenceID}}}, nil
}

// fakeInspectPort 记录 inspect 调用，证明 parser 拒绝发生在适配器之前。
type fakeInspectPort struct {
	calls       int
	lastScope   domain.EvidenceScope
	lastCommand string
	result      domain.SSHInspectResult
	err         error
}

func (f *fakeInspectPort) Inspect(_ context.Context, scope domain.EvidenceScope, req domain.SSHInspectRequest) (domain.SSHInspectResult, error) {
	f.calls++
	f.lastScope = scope
	f.lastCommand = req.Command
	if f.err != nil {
		return domain.SSHInspectResult{}, f.err
	}
	if f.result.Command != "" {
		return f.result, nil
	}
	return domain.SSHInspectResult{Command: req.Command, ExitCode: 0, Stdout: "ok"}, nil
}

// fakeRuntimeEvidenceWriter 记录 AppendEvidence 调用；可按序注入失败，并统计
// 每次写入的 canonical payload 与 ownership，供持久化顺序/幂等断言使用。
type fakeRuntimeEvidenceWriter struct {
	evidence []domain.StoredEvidence
	errs     []error
}

func (w *fakeRuntimeEvidenceWriter) AppendEvidence(_ context.Context, evidence domain.StoredEvidence) (domain.StoredEvidence, error) {
	if len(w.errs) > 0 {
		err := w.errs[0]
		w.errs = w.errs[1:]
		if err != nil {
			return domain.StoredEvidence{}, err
		}
	}
	if evidence.EvidenceID == "" {
		evidence.EvidenceID = fmt.Sprintf("ev-runtime-%d", len(w.evidence)+1)
	}
	w.evidence = append(w.evidence, evidence)
	return evidence, nil
}

// scriptedModel is a fake LLMProviderPort that replays canned envelope JSON in
// order, letting a test drive the coordinator through any state path.
type scriptedModel struct {
	responses      []string
	turns          []domain.ModelTurn
	provider       string
	model          string
	usageTokensIn  int64
	usageTokensOut int64
	usageCostCents int64
	calls          int
}

type nativeScriptedModel struct {
	results []domain.ModelResult
	turns   []domain.ModelTurn
	calls   int
}

func (m *nativeScriptedModel) Complete(_ context.Context, req domain.ModelTurn) (domain.ModelResult, error) {
	turn := req
	turn.Messages = append([]domain.ModelMessage(nil), req.Messages...)
	turn.Tools = append([]domain.ToolDefinition(nil), req.Tools...)
	m.turns = append(m.turns, turn)
	if m.calls >= len(m.results) {
		return domain.ModelResult{}, fmt.Errorf("native scripted model exhausted after %d calls", m.calls)
	}
	result := m.results[m.calls]
	m.calls++
	return result, nil
}

func (m *scriptedModel) Complete(_ context.Context, req domain.ModelTurn) (domain.ModelResult, error) {
	turn := req
	turn.Messages = append([]domain.ModelMessage(nil), req.Messages...)
	turn.Tools = append([]domain.ToolDefinition(nil), req.Tools...)
	m.turns = append(m.turns, turn)
	if m.calls >= len(m.responses) {
		return domain.ModelResult{}, fmt.Errorf("scripted model exhausted after %d calls", m.calls)
	}
	r := m.responses[m.calls]
	m.calls++
	provider := m.provider
	if provider == "" {
		provider = "fake"
	}
	model := m.model
	if model == "" {
		model = "scripted-diagnosis"
	}
	tokensIn := m.usageTokensIn
	tokensOut := m.usageTokensOut
	if tokensIn == 0 && tokensOut == 0 {
		tokensOut = 42
	}
	return domain.ModelResult{
		Content:        r,
		Provider:       provider,
		Model:          model,
		UsageTokensIn:  tokensIn,
		UsageTokensOut: tokensOut,
		UsageCostCents: m.usageCostCents,
		FinishReason:   "stop",
	}, nil
}

type fakeEvidenceResolver struct{}

func (fakeEvidenceResolver) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	records := make([]domain.EvidenceRecord, 0, len(citations))
	for _, citation := range citations {
		records = append(records, domain.EvidenceRecord{
			EvidenceID: citation.EvidenceID, Classification: domain.EvidenceDirectFault,
			Available: true, TemporalCorrelation: true, OperationalCorrelation: true,
		})
	}
	return domain.EvidenceResolution{
		Records:     records,
		Sources:     []domain.SourceCoverage{{SourceID: "source-1", Kind: "test", Primary: true, Status: domain.SourceInspectedSuccess}},
		Time:        &domain.TimeAssessment{OriginalValues: []string{"2026-08-24T07:37:07.956Z"}, Basis: "paired_epoch", Certainty: "high"},
		Correlation: &domain.CorrelationAssessment{Temporal: true, Operational: true, HostIdentity: true},
	}, nil
}

// --- envelope JSON builders ---

func diagnosisEnvelope(fixability string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{`+
		`"fixability":%q,"confidence":0.9,"causalReasoning":"root cause",`+
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":["ev-1"],`+
		`"recommendedNextAction":"next","alertQuality":"enriched",`+
		`"causalClosure":{"explainsOriginalSymptom":true,"explanation":"root cause explains the alert"}}}`, fixability)
}

func evidenceRefDiagnosisEnvelope() string {
	return `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"need evidence",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":[{"evidenceRef":"ev-1"}],` +
		`"recommendedNextAction":"collect"}}`
}

func insufficientWithCollectEnvelope() string {
	return `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.3,"causalReasoning":"need more",` +
		`"contradictions":[],"missingEvidence":["logs"],"evidenceCitations":[],` +
		`"recommendedNextAction":"collect","collectMoreContext":{"reason":"need logs",` +
		`"toolCalls":[{"toolName":"repository.read_file","parameters":{"path":"main.go"}}]}}}`
}

func insufficientWithoutCollectionEnvelope() string {
	return `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.3,"causalReasoning":"no available evidence can close the gap",` +
		`"contradictions":[],"missingEvidence":["runtime logs"],"evidenceCitations":[],` +
		`"recommendedNextAction":"manual review"}}`
}

func insufficientWithClosedCausalClosureEnvelope() string {
	return `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.8,"causalReasoning":"fault invocation explains the alert but test approval is unknown",` +
		`"contradictions":[],"missingEvidence":["approved test identity"],"evidenceCitations":["ev-1"],` +
		`"recommendedNextAction":"verify test authorization","testSuspected":true,"testPolicyMatched":false,` +
		`"causalClosure":{"explainsOriginalSymptom":true,"explanation":"runtime fault invocation produced the observed panic"}}}`
}

func planEnvelope() string {
	return `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{` +
		`"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],` +
		`"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],` +
		`"recommendedId":"p1","rationale":"simplest fix","suggestedDiff":"diff --git a/main.go"}}`
}

// mismatchedClassificationEnvelope 返回带错误 classification 声明的 diagnosis：
// fakeEvidenceResolver 的存储权威分类是 direct_fault，模型声明
// correlated_supporting 触发 R7 的 evidence_correction challenge 路径。
func mismatchedClassificationEnvelope(fixability string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{`+
		`"fixability":%q,"confidence":0.9,"causalReasoning":"root cause",`+
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":[{"evidenceId":"ev-1","classification":"correlated_supporting"}],`+
		`"recommendedNextAction":"next","alertQuality":"enriched",`+
		`"causalClosure":{"explainsOriginalSymptom":true,"explanation":"root cause explains the alert"}}}`, fixability)
}

func requestToolEnvelope(tool, path string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{`+
		`"toolName":%q,"parameters":{"path":%q}}}`, tool, path)
}

func requestEvidenceSearchEnvelope() string {
	return `{"schemaVersion":"v1","kind":"requestTool","requestTool":{` +
		`"toolName":"evidence.search","parameters":{"level":"error"}}}`
}

func requestSSHInspectEnvelope(command string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{`+
		`"toolName":"ssh.inspect","parameters":{"command":%q}}}`, command)
}

func stopEnvelope() string {
	return `{"schemaVersion":"v1","kind":"stop","stop":{"reason":"cannot proceed","recommendedNextAction":"ask an operator to collect the missing runtime evidence"}}`
}
