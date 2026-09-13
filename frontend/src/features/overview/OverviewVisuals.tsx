import type { Overview } from "./api";
import { isTerminal, number, stages, stateLabel } from "./format";

export function OutcomeDial({ overview }: { overview: Overview }) {
  const { successful, failed } = overview.counts;
  const total = successful + failed;
  const ratio = total ? successful / total : 0;
  const circumference = 2 * Math.PI * 72;
  return <div className="hud-outcome">
    <svg viewBox="0 0 200 200" role="img" aria-label={`Delivery success: ${successful} successful, ${failed} failed`}>
      <circle className="hud-dial-rim" cx="100" cy="100" r="94" />
      <circle className="hud-dial-ticks" cx="100" cy="100" r="86" />
      <circle className="hud-dial-base" cx="100" cy="100" r="72" />
      {total > 0 && <><circle className="hud-dial-failed" cx="100" cy="100" r="72" />
        <circle className="hud-dial-success" cx="100" cy="100" r="72" strokeDasharray={`${ratio * circumference} ${circumference}`} transform="rotate(-90 100 100)" /></>}
      <text className="hud-dial-number" x="100" y="99" textAnchor="middle">{total ? Math.round(ratio * 100) : "—"}<tspan className="hud-dial-unit">{total ? "%" : ""}</tspan></text>
      <text className="hud-dial-label" x="100" y="121" textAnchor="middle">DELIVERY</text>
    </svg>
    <div className="hud-dial-legend"><span><i />Success <b>{number(successful)}</b></span><span><i />Failed <b>{number(failed)}</b></span></div>
  </div>;
}

export function StageLoad({ overview, onStage }: { overview: Overview; onStage: (state: string) => void }) {
  const active = overview.stages.filter((stage) => !isTerminal(stage.state));
  const max = Math.max(1, ...active.map((stage) => stage.count));
  return <div className="hud-stage-load" aria-label="Active tasks by stage">
    {active.length ? active.map((stage) => <button key={stage.state} onClick={() => onStage(stage.state)} title={stateLabel(stage.state)}>
      <span>{stages.find((item) => item.id === stage.state)?.short ?? stage.state}</span>
      <span className="hud-bar-track"><i style={{ width: `${stage.count / max * 100}%` }} /></span>
      <b>{number(stage.count)}</b>
    </button>) : <div className="hud-standby"><span />STANDBY</div>}
  </div>;
}

export function FailureBars({ overview }: { overview: Overview }) {
  const max = Math.max(1, ...overview.attention.failures.map((failure) => failure.count));
  return <div className="hud-failure-bars" aria-label="Failure reasons">
    {overview.attention.failures.length ? overview.attention.failures.map((failure) => <div key={failure.reason}>
      <span title={failure.reason.replaceAll("_", " ")}>{failure.reason.replaceAll("_", " ")}</span><b>{number(failure.count)}</b>
      <div className="hud-bar-track"><i style={{ width: `${failure.count / max * 100}%` }} /></div>
    </div>) : <div className="hud-standby"><span />CLEAR</div>}
  </div>;
}
