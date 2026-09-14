import { CircleHelp, Cpu, FileSearch, GitBranch, Layers, Webhook } from "lucide-react";
import type { ProjectConfigurationDraft } from "../../../api";

export function ReviewStep({ configuration, inboundUrl }: { configuration: ProjectConfigurationDraft; inboundUrl?: string | null }) {
  const triggerUrl = inboundUrl ?? configuration.trigger?.inboundUrl;

  return (
    <section>
      <div className="setup-card-title">
        <div className="setup-card-icon">
          <CircleHelp size={18} />
        </div>
        <h2>Review</h2>
      </div>

      <div className="review-summary-grid">
        {/* Environment */}
        <div className="review-block">
          <div className="review-block-header">
            <Layers size={16} />
            <h3>Environment</h3>
          </div>
          <div className="review-block-body">
            <div className="review-row">
              <span>Key</span>
              <code>{configuration.environment?.key ?? "Not saved"}</code>
            </div>
            <div className="review-row">
              <span>Name</span>
              <strong>{configuration.environment?.name ?? "Not saved"}</strong>
            </div>
            <div className="review-row">
              <span>Service</span>
              <code>{configuration.environment?.service || "No service"}</code>
            </div>
          </div>
        </div>

        {/* Git repository */}
        <div className="review-block">
          <div className="review-block-header">
            <GitBranch size={16} />
            <h3>Git repository</h3>
          </div>
          <div className="review-block-body">
            <div className="review-row">
              <span>Remote</span>
              <code>{configuration.repository?.remoteUrl ?? "Not saved"}</code>
            </div>
            <div className="review-row">
              <span>Provider</span>
              <span>{configuration.repository?.scmProvider ?? "generic"} ({configuration.repository?.transport?.toUpperCase() ?? "HTTPS"})</span>
            </div>
            <div className="review-row">
              <span>Baseline</span>
              <span>
                {configuration.repository?.productionBranch ? (
                  <>
                    <strong>{configuration.repository.productionBranch}</strong>
                    {configuration.repository.deployedCommit && (
                      <> @ <code>{configuration.repository.deployedCommit.slice(0, 7)}</code></>
                    )}
                  </>
                ) : (
                  "Not saved"
                )}
              </span>
            </div>
            <div className="review-row">
              <span>Credential</span>
              <span>{configuration.repository?.credentialSecretId ? "Credential linked" : "No credential"}</span>
            </div>
          </div>
        </div>

        {/* Collection source */}
        <div className="review-block">
          <div className="review-block-header">
            <FileSearch size={16} />
            <h3>Collection source</h3>
          </div>
          <div className="review-block-body">
            <div className="review-row">
              <span>Kind</span>
              <strong>{configuration.source?.kind ?? "Not saved"}</strong>
            </div>
            <div className="review-row">
              <span>Credential</span>
              <span>{configuration.source?.credentialSecretId ? "Credential linked" : "No credential"}</span>
            </div>
            <div className="review-row">
              <span>Capabilities</span>
              <span>{configuration.source?.capabilities.join(", ") || "None selected"}</span>
            </div>
          </div>
        </div>

        {/* Trigger */}
        <div className="review-block">
          <div className="review-block-header">
            <Webhook size={16} />
            <h3>Trigger</h3>
          </div>
          <div className="review-block-body">
            <div className="review-row">
              <span>Kind</span>
              <strong>{configuration.trigger?.kind ?? "Not saved"}</strong>
            </div>
            {configuration.trigger?.kind === "signed_webhook" && triggerUrl && (
              <div className="review-row">
                <span>Inbound URL</span>
                <code>{triggerUrl}</code>
              </div>
            )}
          </div>
        </div>

        {/* LLM provider */}
        <div className="review-block">
          <div className="review-block-header">
            <Cpu size={16} />
            <h3>LLM provider</h3>
          </div>
          <div className="review-block-body">
            <div className="review-row">
              <span>Provider</span>
              <strong>{configuration.llm?.provider ?? "Not configured"}</strong>
            </div>
            <div className="review-row">
              <span>Model</span>
              <code>{configuration.llm?.model ?? "Not configured"}</code>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
