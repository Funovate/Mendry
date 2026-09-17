package application

import (
	"strings"

	"mendry/backend/internal/modules/remediation/domain"
)

func publicationTargetBranch(tracker *resilientRunState, fallback LifecyclePublicationPolicy) string {
	if tracker != nil && tracker.publicationPolicy != nil {
		return tracker.publicationPolicy.TargetBranch
	}
	return checkpointPublicationPolicy(fallback).TargetBranch
}

func targetDivergenceSummary(diverged bool, summary string) string {
	marker := "target_diverged=false:"
	if diverged {
		marker = "target_diverged=true:"
	}
	summary = boundedContinuationText(strings.TrimSpace(summary), 900)
	if summary == "" {
		return marker
	}
	return marker + " " + summary
}

func hasTargetDivergence(summary string) bool {
	return strings.HasPrefix(strings.TrimSpace(summary), "target_diverged=true:")
}

func targetDivergenceState(summary string) (bool, bool) {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, "target_diverged=true:") {
		return true, true
	}
	if strings.HasPrefix(summary, "target_diverged=false:") {
		return false, true
	}
	return false, false
}

func checkpointPublicationPolicy(policy LifecyclePublicationPolicy) *domain.CheckpointPublicationPolicy {
	target := strings.TrimSpace(policy.TargetBranch)
	prefix := strings.TrimSuffix(strings.TrimSpace(policy.BranchPrefix), "/")
	if prefix == "" {
		prefix = defaultPublicationPrefix
	}
	return &domain.CheckpointPublicationPolicy{TargetBranch: target, BranchPrefix: prefix}
}
