import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LogRuleTrial } from "../src/api";
import type { CustomRuleDraft } from "../src/features/configuration/configuration";
import { TriggerStep } from "../src/features/configuration/wizard/TriggerStep";

const rule: CustomRuleDraft = {
  id: "errors", name: "Errors", matchType: "contains", pattern: "ERROR", excludePattern: "",
  threshold: 1, windowSeconds: 60, cooldownSeconds: 300,
};

function TrialStep({ onTrial, initialRules = [rule] }: { onTrial: (positive: string[], negative: string[]) => Promise<LogRuleTrial>; initialRules?: CustomRuleDraft[] }) {
  const [rules, setRules] = useState(initialRules);
  return <TriggerStep
    triggerKind="custom_rule" setTriggerKind={() => {}}
    webhookProvider="generic" setWebhookProvider={() => {}}
    awsTopicArn="" setAwsTopicArn={() => {}}
    groupingWindowSeconds={300} setGroupingWindowSeconds={() => {}}
    customRules={rules} setCustomRules={setRules}
    ruleIntent="" setRuleIntent={() => {}} ruleSample="" setRuleSample={() => {}}
    onGenerateRule={() => {}} onTrialLogRules={onTrial}
    onInstallLogProbe={() => {}} onRefreshLogProbe={() => {}} onUninstallLogProbe={() => {}}
    onSave={() => {}}
  />;
}

afterEach(cleanup);

describe("log rule trial", () => {
  it("shows missed and unexpected matches, then invalidates results after editing", async () => {
    const onTrial = vi.fn(async () => ({
      positiveCount: 2, negativeCount: 1,
      rules: [{ ruleId: "errors", positiveMatches: [1], negativeMatches: [1], positiveExcluded: [], negativeExcluded: [] }],
    }));
    render(<TrialStep onTrial={onTrial} />);
    fireEvent.change(screen.getByLabelText("Expected errors"), { target: { value: "ERROR payment\nINFO payment" } });
    fireEvent.change(screen.getByLabelText("Expected non-errors"), { target: { value: "ERROR known warning" } });
    fireEvent.click(screen.getByRole("button", { name: "Run test" }));
    await waitFor(() => expect(screen.getByText("Missed error lines: 2")).toBeInTheDocument());
    expect(screen.getByText("Unexpected match lines: 1")).toBeInTheDocument();
    expect(onTrial).toHaveBeenCalledWith(["ERROR payment", "INFO payment"], ["ERROR known warning"]);
    fireEvent.change(screen.getByLabelText("Expected non-errors"), { target: { value: "INFO payment" } });
    expect(screen.queryByText("Missed error lines: 2")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run test" }));
    await waitFor(() => expect(screen.getByText("Missed error lines: 2")).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Text or pattern"), { target: { value: "FATAL" } });
    expect(screen.queryByText("Missed error lines: 2")).not.toBeInTheDocument();
  });

  it("treats coverage from independent rules as a combined result", async () => {
    const onTrial = vi.fn(async () => ({
      positiveCount: 2, negativeCount: 1,
      rules: [
        { ruleId: "errors", positiveMatches: [1], negativeMatches: [], positiveExcluded: [], negativeExcluded: [] },
        { ruleId: "warnings", positiveMatches: [2], negativeMatches: [], positiveExcluded: [], negativeExcluded: [] },
      ],
    }));
    render(<TrialStep onTrial={onTrial} initialRules={[rule, { ...rule, id: "warnings", name: "Warnings", pattern: "WARN" }]} />);
    fireEvent.change(screen.getByLabelText("Expected errors"), { target: { value: "ERROR payment\nWARN disk" } });
    fireEvent.change(screen.getByLabelText("Expected non-errors"), { target: { value: "INFO payment" } });
    fireEvent.click(screen.getByRole("button", { name: "Run test" }));
    await waitFor(() => expect(screen.getByText("Matches these samples")).toBeInTheDocument());
    expect(screen.getByText("Errors: 2/2 matched · Non-errors: 0/1 matched")).toBeInTheDocument();
  });
});
