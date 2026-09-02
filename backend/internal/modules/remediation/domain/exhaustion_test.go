package domain_test

import (
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

// validExhaustionProposal 构造一个满足 v1 边界的 D6 exhaustion proof；测试
// 通过修改字段制造违规。schema 版本由外层 envelope 携带，payload 只含 D6
// 的五个字段。
func validExhaustionProposal() domain.ExhaustionProposalV1 {
	return domain.ExhaustionProposalV1{
		UnresolvedGoal: "no collectible evidence can close the causal gap",
		AttemptedPaths: []domain.ExhaustionAttemptedPath{{
			Capability:  "repository",
			OutcomeRefs: []string{"action:repository:1"},
		}},
		UntriedCapabilities: []domain.ExhaustionUntriedCapability{{
			Capability:   "runtime_logs",
			ReasonCode:   string(domain.ExhaustionReasonUnavailable),
			EvidenceRefs: []string{"ev-1"},
		}},
		BestConclusion: domain.FixabilityInsufficientEvidence,
		Handoff:        "ask an operator to collect the missing runtime evidence",
	}
}

func TestExhaustionReasonCodeIsKnown(t *testing.T) {
	known := []domain.ExhaustionReasonCode{
		domain.ExhaustionReasonUnavailable,
		domain.ExhaustionReasonIrrelevant,
		domain.ExhaustionReasonUnsafe,
		domain.ExhaustionReasonBudgetProhibited,
	}
	for _, code := range known {
		if !code.IsKnown() {
			t.Errorf("reason code %q must be known", code)
		}
	}
	if domain.ExhaustionReasonCode("exhausted").IsKnown() {
		t.Fatal("unknown reason code must not be known")
	}
}

func TestKnownCapabilityClassesCoverD1StableSet(t *testing.T) {
	classes := domain.KnownCapabilityClasses()
	want := []string{
		"repository", "provider_evidence", "runtime_logs", "ssh_inspect",
		"workspace", "validation", "publication",
	}
	if len(classes) != len(want) {
		t.Fatalf("KnownCapabilityClasses() = %v, want %v", classes, want)
	}
	for _, class := range want {
		found := false
		for _, candidate := range classes {
			if candidate == class {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("KnownCapabilityClasses() missing %q: %v", class, classes)
		}
	}
}

func TestExhaustionProposalV1Validate(t *testing.T) {
	t.Run("valid proposal passes", func(t *testing.T) {
		if err := validExhaustionProposal().Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*domain.ExhaustionProposalV1)
	}{
		{"empty unresolved goal", func(p *domain.ExhaustionProposalV1) { p.UnresolvedGoal = " " }},
		{"unresolved goal too long", func(p *domain.ExhaustionProposalV1) {
			p.UnresolvedGoal = strings.Repeat("x", 1025)
		}},
		{"empty handoff", func(p *domain.ExhaustionProposalV1) { p.Handoff = "" }},
		{"handoff too long", func(p *domain.ExhaustionProposalV1) {
			p.Handoff = strings.Repeat("x", 513)
		}},
		{"unknown best conclusion", func(p *domain.ExhaustionProposalV1) {
			p.BestConclusion = domain.FixabilityClass("definitely_fixable")
		}},
		{"empty attempted capability", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths[0].Capability = ""
		}},
		{"unknown attempted capability", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths[0].Capability = "tcpdump"
		}},
		{"too many attempted paths", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths = make([]domain.ExhaustionAttemptedPath, 17)
			for i := range p.AttemptedPaths {
				p.AttemptedPaths[i] = domain.ExhaustionAttemptedPath{Capability: "repository"}
			}
		}},
		{"too many outcome refs", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths[0].OutcomeRefs = make([]string, 33)
			for i := range p.AttemptedPaths[0].OutcomeRefs {
				p.AttemptedPaths[0].OutcomeRefs[i] = "ref"
			}
		}},
		{"empty outcome ref", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths[0].OutcomeRefs = []string{"   "}
		}},
		{"outcome ref too long", func(p *domain.ExhaustionProposalV1) {
			p.AttemptedPaths[0].OutcomeRefs = []string{strings.Repeat("x", 257)}
		}},
		{"unknown untried capability", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities[0].Capability = "docker.shell"
		}},
		{"empty untried reason code", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities[0].ReasonCode = ""
		}},
		{"unknown untried reason code", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities[0].ReasonCode = "exhausted"
		}},
		{"too many untried capabilities", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities = make([]domain.ExhaustionUntriedCapability, 17)
			for i := range p.UntriedCapabilities {
				p.UntriedCapabilities[i] = domain.ExhaustionUntriedCapability{
					Capability: "runtime_logs", ReasonCode: string(domain.ExhaustionReasonUnavailable),
				}
			}
		}},
		{"too many evidence refs", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities[0].EvidenceRefs = make([]string, 33)
			for i := range p.UntriedCapabilities[0].EvidenceRefs {
				p.UntriedCapabilities[0].EvidenceRefs[i] = "ev"
			}
		}},
		{"empty evidence ref", func(p *domain.ExhaustionProposalV1) {
			p.UntriedCapabilities[0].EvidenceRefs = []string{""}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proposal := validExhaustionProposal()
			tt.mutate(&proposal)
			if err := proposal.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestExhaustionProposalRequiresAuthorityRefs(t *testing.T) {
	proposal := validExhaustionProposal()
	proposal.AttemptedPaths[0].OutcomeRefs = nil
	if err := proposal.Validate(); err == nil || !strings.Contains(err.Error(), "requires an outcome ref") {
		t.Fatalf("Validate() error = %v, want required action outcome ref", err)
	}

	proposal = validExhaustionProposal()
	proposal.UntriedCapabilities[0].EvidenceRefs = nil
	if err := proposal.Validate(); err == nil || !strings.Contains(err.Error(), "requires factual evidence refs") {
		t.Fatalf("Validate() error = %v, want factual evidence refs", err)
	}

	proposal = validExhaustionProposal()
	proposal.UntriedCapabilities[0] = domain.ExhaustionUntriedCapability{
		Capability: "runtime_logs", ReasonCode: string(domain.ExhaustionReasonBudgetProhibited),
	}
	if err := proposal.Validate(); err != nil {
		t.Fatalf("budget-prohibited capability should not require evidence refs: %v", err)
	}
}
