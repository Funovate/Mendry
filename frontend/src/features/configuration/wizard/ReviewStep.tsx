import { CircleHelp } from "lucide-react";
import type { ProjectConfiguration } from "../../../api";

export function ReviewStep({ configuration }: { configuration: ProjectConfiguration }) {
  return <section>
    <div className="setup-card-title"><CircleHelp size={20} /><div><h2>Review</h2><p>Confirm every section before saving — this is the exact snapshot that will be written.</p></div></div>
    <div className="review-summary">
      <div>
        <h3>Environment</h3>
        <dl>
          <dt>Key</dt><dd>{configuration.environment.key}</dd>
          <dt>Name</dt><dd>{configuration.environment.name}</dd>
          <dt>Service</dt><dd>{configuration.environment.service || "No service"}</dd>
        </dl>
      </div>
      <div>
        <h3>Git repository</h3>
        <dl>
          <dt>Remote</dt><dd>{configuration.repository.remoteUrl}</dd>
          <dt>SCM provider</dt><dd>{configuration.repository.scmProvider}</dd>
          <dt>Transport</dt><dd>{configuration.repository.transport}</dd>
          <dt>Credential</dt><dd>{configuration.repository.credentialSecretId || "No credential"}</dd>
          <dt>Production branch</dt><dd>{configuration.repository.productionBranch}</dd>
          <dt>Deployed commit</dt><dd>{configuration.repository.deployedCommit}</dd>
        </dl>
      </div>
      <div>
        <h3>Collection source</h3>
        <dl>
          <dt>Name</dt><dd>{configuration.source.name}</dd>
          <dt>Kind</dt><dd>{configuration.source.kind}</dd>
          <dt>Credential</dt><dd>{configuration.source.credentialSecretId || "No credential"}</dd>
          <dt>Capabilities</dt><dd>{configuration.source.capabilities.join(", ") || "None selected"}</dd>
        </dl>
      </div>
      <div>
        <h3>Trigger</h3>
        <dl>
          <dt>Name</dt><dd>{configuration.trigger.name}</dd>
          <dt>Kind</dt><dd>{configuration.trigger.kind}</dd>
          <dt>Signing credential</dt><dd>{configuration.trigger.signingSecretId || "No credential"}</dd>
        </dl>
      </div>
    </div>
  </section>;
}
