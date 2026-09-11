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

func checkpointPublicationPolicy(policy LifecyclePublicationPolicy) *domain.CheckpointPublicationPolicy {
	target := strings.TrimSpace(policy.TargetBranch)
	if target == "" {
		target = defaultPublicationTarget
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(policy.BranchPrefix), "/")
	if prefix == "" {
		prefix = defaultPublicationPrefix
	}
	return &domain.CheckpointPublicationPolicy{TargetBranch: target, BranchPrefix: prefix}
}
