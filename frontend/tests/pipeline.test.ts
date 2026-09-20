import type { RemediationReview } from "../src/api";
import { describe, expect, it } from "vitest";
import {
  resolveActionability,
  WORKFLOW_STAGES,
} from "../src/features/board/PipelinePage";

describe("Pipeline Actionability & Stage Mapping", () => {
  it("places diagnosis_ready_for_review in Diagnosis stage", () => {
    const diagnosisStage = WORKFLOW_STAGES.find((s) => s.id === "diagnosis");
    expect(diagnosisStage?.states).toContain("diagnosis_ready_for_review");
  });

  it("places blocked_manual_review in Remediation stage", () => {
    const remediationStage = WORKFLOW_STAGES.find((s) => s.id === "remediation");
    expect(remediationStage?.states).toContain("blocked_manual_review");
  });

  it("classifies autonomous states as running and auto-advancing", () => {
    const activeStates = [
      "queued",
      "preparing_context",
      "collecting_more_context",
      "diagnosing",
      "planning",
      "patching",
      "running",
      "validating",
      "publishing",
    ];

    activeStates.forEach((state) => {
      const result = resolveActionability(state, null);
      expect(result.category).toBe("running");
      expect(result.badgeTone).toBe("running");
      expect(result.badgeText).toBe("Auto-Advancing");
      expect(result.isAutoAdvancing).toBe(true);
      expect(result.canAdvance).toBe(true);
      expect(result.quickActionLabel).toBeUndefined();
    });
  });

  it("classifies diagnosis_ready_for_review as needs_action requiring plan review", () => {
    const result = resolveActionability("diagnosis_ready_for_review", null);
    expect(result.category).toBe("needs_action");
    expect(result.badgeTone).toBe("action");
    expect(result.badgeText).toBe("Action Required");
    expect(result.isAutoAdvancing).toBe(false);
    expect(result.canAdvance).toBe(true);
    expect(result.quickActionLabel).toBe("Review Plan ➔");
  });

  it("classifies awaiting_human_review as an explicit Git handoff", () => {
    const review = {
      checkpoint: {
        validations: [{ commandId: "test", commandVersion: 1, treeHash: "tree", passed: true }],
        publication: {
          branchRef: "hotfix/incident-1",
          targetBranch: "main",
          commitHash: "abc123",
          changeRef: "PR #42",
          compareUrl: "https://git.example/pull/42",
        },
      },
    } as unknown as RemediationReview;

    const result = resolveActionability("awaiting_human_review", review);
    expect(result.category).toBe("needs_action");
    expect(result.badgeTone).toBe("action");
    expect(result.badgeText).toBe("Operator Action Required");
    expect(result.completedText).toContain("validation passed");
    expect(result.actor).toBe("Operator");
    expect(result.isAutoAdvancing).toBe(false);
    expect(result.canAdvance).toBe(true);
    expect(result.quickActionLabel).toBe("Open Git review");
    expect(result.actionUrl).toBe("https://git.example/pull/42");
    expect(result.nextStepText).toContain("mark the incident Recovered or Closed");
  });

  it("does not claim validation or a PR when delivery only has a branch", () => {
    const review = {
      checkpoint: {
        validations: [],
        publication: {
          branchRef: "hotfix/incident-1",
          targetBranch: "release",
          commitHash: "abc123",
        },
      },
    } as unknown as RemediationReview;

    const result = resolveActionability("awaiting_human_review", review);
    expect(result.completedText).toContain("no passing local validation record");
    expect(result.nextStepText).toContain("hotfix/incident-1");
    expect(result.nextStepText).toContain("release");
    expect(result.quickActionLabel).toBe("View Git instructions");
    expect(result.actionUrl).toBeUndefined();
  });

  it("classifies blocked_manual_review as blocked requiring operator takeover", () => {
    const mockReview = {
      manualSuggestion: "Touch sensitive auth directory prohibited",
    } as unknown as RemediationReview;
    const result = resolveActionability("blocked_manual_review", mockReview);
    expect(result.category).toBe("blocked");
    expect(result.badgeTone).toBe("blocked");
    expect(result.badgeText).toBe("Policy Blocked");
    expect(result.isAutoAdvancing).toBe(false);
    expect(result.canAdvance).toBe(false);
    expect(result.quickActionLabel).toBe("Take Over ➔");
    expect(result.statusDescription).toContain("Touch sensitive auth directory prohibited");
  });

  it("classifies retryable failed/budget_exhausted runs as recoverable", () => {
    const mockRetryable = {
      retryable: true,
      terminalReason: "Subprocess timeout",
    } as unknown as RemediationReview;

    const failedResult = resolveActionability("failed", mockRetryable);
    expect(failedResult.category).toBe("recoverable");
    expect(failedResult.badgeTone).toBe("recoverable");
    expect(failedResult.badgeText).toBe("Recoverable Error");
    expect(failedResult.isAutoAdvancing).toBe(false);
    expect(failedResult.canAdvance).toBe(true);
    expect(failedResult.quickActionLabel).toBe("Retry Run ➔");

    const budgetResult = resolveActionability("budget_exhausted", mockRetryable);
    expect(budgetResult.category).toBe("recoverable");
    expect(budgetResult.badgeTone).toBe("recoverable");
    expect(budgetResult.badgeText).toBe("Budget Exceeded");
    expect(budgetResult.quickActionLabel).toBe("Resume Run ➔");
  });

  it("classifies non-retryable failed runs as terminal", () => {
    const mockTerminal = {
      retryable: false,
      continuationAvailable: false,
      terminalReason: "Repository corrupted",
    } as unknown as RemediationReview;

    const result = resolveActionability("failed", mockTerminal);
    expect(result.category).toBe("terminal");
    expect(result.badgeTone).toBe("terminal");
    expect(result.badgeText).toBe("Failed (Terminated)");
    expect(result.canAdvance).toBe(false);
    expect(result.quickActionLabel).toBeUndefined();
  });

  it("classifies completed_non_code as terminal", () => {
    const result = resolveActionability("completed_non_code", null);
    expect(result.category).toBe("terminal");
    expect(result.badgeTone).toBe("terminal");
    expect(result.badgeText).toBe("Resolved");
    expect(result.actor).toBe("Operator");
    expect(result.statusDescription).not.toContain("successfully closed");
    expect(result.nextStepText).toContain("mark the incident Recovered or Closed");
    expect(result.canAdvance).toBe(false);
  });

  it("classifies undefined status as unstarted backlog", () => {
    const result = resolveActionability(undefined, null);
    expect(result.category).toBe("unstarted");
    expect(result.badgeTone).toBe("unstarted");
    expect(result.badgeText).toBe("Unstarted");
    expect(result.isAutoAdvancing).toBe(false);
    expect(result.canAdvance).toBe(true);
    expect(result.quickActionLabel).toBe("Start Fix ➔");
  });
});
