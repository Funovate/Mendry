import { CircleHelp } from "lucide-react";
import type { ProjectConfigurationDraft } from "../../../api";

export function ReviewStep({ configuration, inboundUrl }: { configuration: ProjectConfigurationDraft; inboundUrl?: string | null }) {
  const triggerUrl = inboundUrl ?? configuration.trigger?.inboundUrl;
  return <section>
    <div className="setup-card-title"><CircleHelp size={20} /><div><h2>Review</h2><p>Review the saved state of each configuration component.</p></div></div>
    <div className="review-summary">
      <div>
        <h3>Environment</h3>
        <dl>
          <dt>Key</dt><dd>{configuration.environment?.key ?? "Not saved"}</dd>
          <dt>Name</dt><dd>{configuration.environment?.name ?? "Not saved"}</dd>
          <dt>Service</dt><dd>{configuration.environment?.service || "No service"}</dd>
        </dl>
      </div>
      <div>
        <h3>Git repository</h3>
        <dl>
          <dt>Remote</dt><dd>{configuration.repository?.remoteUrl ?? "Not saved"}</dd>
          <dt>SCM provider</dt><dd>{configuration.repository?.scmProvider ?? "Not saved"}</dd>
          <dt>Transport</dt><dd>{configuration.repository?.transport ?? "Not saved"}</dd>
          <dt>Credential</dt><dd>{configuration.repository?.credentialSecretId || "No credential"}</dd>
          <dt>Production branch</dt><dd>{configuration.repository?.productionBranch ?? "Not saved"}</dd>
          <dt>Deployed commit</dt><dd>{configuration.repository?.deployedCommit ?? "Not saved"}</dd>
        </dl>
      </div>
      <div>
        <h3>Collection source</h3>
        <dl>
          <dt>Kind</dt><dd>{configuration.source?.kind ?? "Not saved"}</dd>
          <dt>Credential</dt><dd>{configuration.source?.credentialSecretId || "No credential"}</dd>
          <dt>Capabilities</dt><dd>{configuration.source?.capabilities.join(", ") || "None selected"}</dd>
        </dl>
      </div>
      <div>
        <h3>Trigger</h3>
        <dl>
          <dt>Kind</dt><dd>{configuration.trigger?.kind ?? "Not saved"}</dd>
          {configuration.trigger?.kind === "signed_webhook" && triggerUrl && <>
            <dt>Inbound URL</dt><dd>{triggerUrl}</dd>
          </>}
        </dl>
      </div>
      <div>
        <h3>LLM provider</h3>
        <dl>
          <dt>Provider</dt><dd>{configuration.llm?.provider ?? "Not configured"}</dd>
          <dt>Model</dt><dd>{configuration.llm?.model ?? "Not configured"}</dd>
        </dl>
      </div>
    </div>
  </section>;
}
