import { AutoHotfixSetup } from "./AutoHotfixSetup";
import {
  AlertCircle,
  Check,
  Cpu,
  FileSearch,
  GitPullRequest,
  LoaderCircle,
  Plus,
  ShieldCheck,
  Sliders,
  Trash2,
} from "lucide-react";
import { messageFromError, type ProjectConfiguration, type ProjectSecret } from "../../../api";

type Policy = ProjectConfiguration["remediation"];
type Command = Policy["validationProfile"]["requiredCommands"][number];

type Props = {
  policy: Policy;
  setPolicy: (policy: Policy) => void;
  secrets: ProjectSecret[];
  onSave: () => void;
  saving: boolean;
  saveError: unknown;
};

const updateCommand = (commands: Command[], index: number, patch: Partial<Command>) =>
  commands.map((command, commandIndex) => commandIndex === index ? { ...command, ...patch } : command);

export function RemediationStep({ policy, setPolicy, secrets, onSave, saving, saveError }: Props) {
  const auto = policy.executionMode === "auto_hotfix";
  const profile = policy.validationProfile;
  const localValidation = profile.enabled !== false;
  const publication = policy.publication;
  const changePolicy = policy.changePolicy;
  const gitSecrets = secrets.filter((secret) => secret.kind === "git_credential");
  const apiSecrets = secrets.filter((secret) => secret.kind === "http_bearer");
  const patchPolicy = (patch: Partial<Policy>) => setPolicy({ ...policy, ...patch });
  const patchProfile = (patch: Partial<Policy["validationProfile"]>) => patchPolicy({ validationProfile: { ...profile, ...patch } });
  const patchPublication = (patch: Partial<Policy["publication"]>) => patchPolicy({ publication: { ...publication, ...patch } });
  const patchChangePolicy = (patch: Partial<Policy["changePolicy"]>) => patchPolicy({ changePolicy: { ...changePolicy, ...patch } });

  const validChangePolicy = (
    changePolicy.allowedPaths.length > 0 &&
    changePolicy.allowedPaths.length <= 64 &&
    changePolicy.deniedPaths.length <= 64 &&
    changePolicy.maxChangedFiles >= 1 &&
    changePolicy.maxChangedFiles <= 30 &&
    changePolicy.maxChangedLines >= 1 &&
    changePolicy.maxChangedLines <= 5000
  );

  const canSave = (!auto || Boolean(
    publication.gitCredentialSecretId && (!localValidation || (
      profile.imageDigest.match(/^sha256:[0-9a-f]{64}$/) &&
      profile.workingDirectory && profile.requiredCommands.length > 0 &&
      profile.requiredCommands.every((command) => command.id && command.argv.length > 0)
    )),
  )) && (!auto || validChangePolicy);

  return (
    <section>
      <div className="setup-card-title">
        <div className="setup-card-title-left">
          <div className="setup-card-icon">
            <ShieldCheck size={20} />
          </div>
          <div className="setup-card-heading">
            <h2>Automatic hotfix</h2>
            <p>Configure automated incident remediation, test validation runner, and pull request policies.</p>
          </div>
        </div>
        <div className="setup-card-badge-right">
          {auto ? (
            <span className="config-badge config-badge-teal">
              <span className="config-dot-pulse" />
              Draft PR Mode
            </span>
          ) : (
            <span className="config-badge config-badge-neutral">
              Analysis Only
            </span>
          )}
        </div>
      </div>

      <div className="mode-section">
        <div className="mode-selection-grid" role="radiogroup" aria-label="Remediation execution mode">
          <button
            type="button"
            role="radio"
            aria-checked={!auto}
            className={`mode-card ${!auto ? "selected" : ""}`}
            onClick={() => patchPolicy({ executionMode: "analysis_only" })}
          >
            <div className="mode-card-header">
              <div className="mode-card-icon">
                <FileSearch size={20} />
              </div>
              <div className="mode-card-badge-wrap">
                <span className="config-badge config-badge-neutral">Safe · Read-only</span>
                <span className={`mode-card-radio ${!auto ? "checked" : ""}`} />
              </div>
            </div>
            <div className="mode-card-body">
              <h4 className="mode-card-title">Analysis only</h4>
              <p className="mode-card-desc">
                Inspect incident telemetry, diagnose root causes, and propose code recommendations in the incident workspace without touching repository branches or submitting pull requests.
              </p>
            </div>
          </button>

          <button
            type="button"
            role="radio"
            aria-checked={auto}
            className={`mode-card ${auto ? "selected" : ""}`}
            onClick={() => patchPolicy({ executionMode: "auto_hotfix", agentLoopMode: "resilient_v1" })}
          >
            <div className="mode-card-header">
              <div className="mode-card-icon mode-icon-pr">
                <GitPullRequest size={20} />
              </div>
              <div className="mode-card-badge-wrap">
                <span className="config-badge config-badge-teal">Automated Hotfix</span>
                <span className={`mode-card-radio ${auto ? "checked" : ""}`} />
              </div>
            </div>
            <div className="mode-card-body">
              <h4 className="mode-card-title">Draft PR</h4>
              <p className="mode-card-desc">
                Synthesize tested code patches, execute local pre-validation test suites, and automatically publish draft Pull Requests on a dedicated remediation branch.
              </p>
            </div>
          </button>
        </div>
      </div>

      {auto && (
        <>
          <div className="step-content-group">
            <div className="feature-toggle-card">
              <div className="feature-toggle-row">
                <div className="feature-toggle-meta">
                  <div className="feature-toggle-icon">
                    <Cpu size={18} />
                  </div>
                  <div>
                    <h4>Mendry local pre-validation</h4>
                    <p className="field-hint">Run test and verification commands in an isolated container runner before opening pull requests.</p>
                  </div>
                </div>
                <label className="toggle-switch" aria-label="Toggle Mendry local pre-validation">
                  <input
                    type="checkbox"
                    checked={localValidation}
                    onChange={(event) => patchProfile({ enabled: event.target.checked })}
                  />
                  <span className="toggle-slider" />
                </label>
              </div>
            </div>
          </div>

          {localValidation && (
            <>
              <AutoHotfixSetup onEnabled={(nextPolicy) => setPolicy(nextPolicy)} />

              <div className="step-content-group">
                <details className="collapsible-section" open={!profile.imageDigest || profile.requiredCommands.length === 0}>
                  <summary className="collapsible-summary">
                    <div className="collapsible-summary-title">
                      <Sliders size={16} />
                      <span>Local validation settings</span>
                    </div>
                    <span className="config-badge config-badge-neutral">Runner & Commands</span>
                  </summary>
                  <div className="collapsible-content">
                    <div className="sub-section-header">
                      <h4>Validation runner</h4>
                      <p className="field-hint">Configure the isolated container image, workspace directory, and resource limits.</p>
                    </div>
                    <div className="form-grid two-col">
                      <label className="field-label">
                        <span>Image digest <span className="field-required">*</span></span>
                        <input
                          value={profile.imageDigest}
                          onChange={(event) => patchProfile({ imageDigest: event.target.value.trim() })}
                          placeholder="sha256:0123456789abcdef..."
                          className={profile.imageDigest && !profile.imageDigest.match(/^sha256:[0-9a-f]{64}$/) ? "input-invalid" : ""}
                        />
                        <span className="field-hint">Full SHA-256 digest of the pre-built container image.</span>
                      </label>
                      <label className="field-label">
                        <span>Working directory</span>
                        <input value={profile.workingDirectory} onChange={(event) => patchProfile({ workingDirectory: event.target.value })} placeholder="." />
                        <span className="field-hint">Relative working directory inside the runner container.</span>
                      </label>
                      <label className="field-label">
                        <span>CPU limit</span>
                        <input type="number" min={1} max={16} value={profile.cpuLimit} onChange={(event) => patchProfile({ cpuLimit: Number(event.target.value) })} />
                      </label>
                      <label className="field-label">
                        <span>Memory limit (MiB)</span>
                        <input type="number" min={256} max={65536} value={profile.memoryLimitMiB} onChange={(event) => patchProfile({ memoryLimitMiB: Number(event.target.value) })} />
                      </label>
                    </div>

                    <div className="commands-sub-section">
                      <div className="section-heading-row">
                        <div>
                          <h4>Required commands</h4>
                          <p className="field-hint">Every command must succeed (exit code 0) for a hotfix to be verified.</p>
                        </div>
                        <button
                          type="button"
                          className="secondary-button"
                          onClick={() => patchProfile({ requiredCommands: [...profile.requiredCommands, { id: "", version: 1, argv: [], timeoutSeconds: 600 }] })}
                        >
                          <Plus size={14} />
                          <span>Add command</span>
                        </button>
                      </div>

                      {profile.requiredCommands.length === 0 && (
                        <div className="empty-sub-state">
                          <p>No validation commands configured. Click &quot;Add command&quot; to configure build, lint, or test commands.</p>
                        </div>
                      )}

                      <div className="command-cards-list">
                        {profile.requiredCommands.map((command, index) => (
                          <div className="command-card" key={`${command.id}-${index}`}>
                            <div className="command-card-header">
                              <span className="command-index-badge">Command #{index + 1}</span>
                              <button
                                type="button"
                                className="icon-button icon-button-danger"
                                title="Remove validation command"
                                onClick={() => patchProfile({ requiredCommands: profile.requiredCommands.filter((_, commandIndex) => commandIndex !== index) })}
                              >
                                <Trash2 size={14} />
                              </button>
                            </div>
                            <div className="form-grid two-col">
                              <label className="field-label">
                                <span>Command ID <span className="field-required">*</span></span>
                                <input
                                  value={command.id}
                                  placeholder="e.g. test-suite"
                                  onChange={(event) => patchProfile({ requiredCommands: updateCommand(profile.requiredCommands, index, { id: event.target.value }) })}
                                />
                              </label>
                              <label className="field-label">
                                <span>Timeout (seconds)</span>
                                <input
                                  type="number"
                                  min={1}
                                  max={3600}
                                  value={command.timeoutSeconds}
                                  onChange={(event) => patchProfile({ requiredCommands: updateCommand(profile.requiredCommands, index, { timeoutSeconds: Number(event.target.value) }) })}
                                />
                              </label>
                              <label className="field-label full-col">
                                <span>Arguments (one per line) <span className="field-required">*</span></span>
                                <textarea
                                  rows={3}
                                  value={command.argv.join("\n")}
                                  placeholder={"npm\ntest"}
                                  className="font-mono-input"
                                  onChange={(event) => patchProfile({ requiredCommands: updateCommand(profile.requiredCommands, index, { argv: event.target.value.split("\n").map((value) => value.trim()).filter(Boolean) }) })}
                                />
                              </label>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>
                </details>
              </div>
            </>
          )}

          <div className="step-content-group">
            <div className="sub-section-header">
              <h3>Publication</h3>
              <p className="field-hint">Credentials and platform API settings for pushing hotfix branches and opening pull requests.</p>
            </div>
            <div className="form-grid two-col">
              <label className="field-label">
                <span>Git write credential <span className="field-required">*</span></span>
                <select value={publication.gitCredentialSecretId} onChange={(event) => patchPublication({ gitCredentialSecretId: event.target.value })}>
                  <option value="">Select credential</option>
                  {gitSecrets.map((secret) => <option key={secret.id} value={secret.id}>{secret.name}</option>)}
                </select>
              </label>
              <label className="field-label">
                <span>Platform API credential</span>
                <select value={publication.apiCredentialSecretId} onChange={(event) => patchPublication({ apiCredentialSecretId: event.target.value })}>
                  <option value="">Use Git credential or none</option>
                  {apiSecrets.map((secret) => <option key={secret.id} value={secret.id}>{secret.name}</option>)}
                </select>
              </label>
              <label className="field-label">
                <span>Platform API base URL</span>
                <input type="url" value={publication.apiBaseUrl} onChange={(event) => patchPublication({ apiBaseUrl: event.target.value.trim() })} placeholder="https://git.example.com/api/v4" />
              </label>
              <label className="field-label">
                <span>Branch prefix</span>
                <input value={publication.branchPrefix} disabled className="readonly-input" />
              </label>
            </div>
          </div>

          <div className="step-content-group">
            <div className="sub-section-header">
              <h3>Change policy</h3>
              <p className="field-hint">Safety boundaries and scope thresholds for automated code patch changes.</p>
            </div>
            <div className="form-grid two-col">
              <label className="field-label">
                <span>Allowed paths (one per line)</span>
                <textarea className="font-mono-input" rows={4} value={changePolicy.allowedPaths.join("\n")} onChange={(event) => patchChangePolicy({ allowedPaths: event.target.value.split("\n").map((value) => value.trim()).filter(Boolean) })} />
              </label>
              <label className="field-label">
                <span>Additional denied paths (one per line)</span>
                <textarea className="font-mono-input" rows={4} value={changePolicy.deniedPaths.join("\n")} onChange={(event) => patchChangePolicy({ deniedPaths: event.target.value.split("\n").map((value) => value.trim()).filter(Boolean) })} />
              </label>
              <label className="field-label">
                <span>Maximum changed files (1–30)</span>
                <input
                  type="number"
                  min={1}
                  max={30}
                  value={changePolicy.maxChangedFiles}
                  className={changePolicy.maxChangedFiles < 1 || changePolicy.maxChangedFiles > 30 ? "input-invalid" : ""}
                  onChange={(event) => patchChangePolicy({ maxChangedFiles: Number(event.target.value) })}
                />
                <span className="field-hint">Maximum files Mendry can modify per patch (1–30).</span>
              </label>
              <label className="field-label">
                <span>Maximum changed lines (1–5000)</span>
                <input
                  type="number"
                  min={1}
                  max={5000}
                  value={changePolicy.maxChangedLines}
                  className={changePolicy.maxChangedLines < 1 || changePolicy.maxChangedLines > 5000 ? "input-invalid" : ""}
                  onChange={(event) => patchChangePolicy({ maxChangedLines: Number(event.target.value) })}
                />
                <span className="field-hint">Maximum cumulative line diff per patch (1–5000).</span>
              </label>
            </div>
          </div>
        </>
      )}

      {saveError != null && <p className="field-error" role="alert">{messageFromError(saveError)}</p>}
      <div className="setup-actions save-policy-actions">
        <button type="button" className="primary-button" disabled={!canSave || saving} onClick={onSave}>
          {saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}
          <span>{saving ? "Saving..." : "Save hotfix policy"}</span>
        </button>
        {!canSave && auto && (
          <div className="policy-requirement-hint" role="note">
            <AlertCircle size={14} />
            <span>
              {!publication.gitCredentialSecretId
                ? "A Git write credential is required to save Draft PR mode."
                : !validChangePolicy
                ? "Change policy thresholds must be within limits (1–30 files, 1–5000 lines, at least one allowed path)."
                : localValidation && (!profile.imageDigest.match(/^sha256:[0-9a-f]{64}$/) || !profile.workingDirectory || profile.requiredCommands.length === 0 || !profile.requiredCommands.every((c) => c.id && c.argv.length > 0))
                ? "Complete validation runner settings (image digest, working directory, and command ID/argv)."
                : "Please fill in the required fields."}
            </span>
          </div>
        )}
      </div>
    </section>
  );
}
