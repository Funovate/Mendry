import { useMemo } from "react";
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { ArrowUpRight } from "lucide-react";
import type { Overview, OverviewTask } from "./api";
import { isTerminal, number, stages, stateLabel, taskDuration } from "./format";

type StageData = { state: string; count: number; tasks: OverviewTask[]; now: number; selected: boolean;
  onStage: (state: string) => void; onTask: (task: OverviewTask) => void };
type StageNode = Node<StageData, "stage">;
function FlowStage({ data }: NodeProps<StageNode>) {
  const tone = ["failed", "budget_exhausted"].includes(data.state) ? "danger" : isTerminal(data.state) ? "waiting" : "active";
  return <div className={`overview-flow-node ${tone} ${data.count ? "occupied" : "empty"} ${data.selected ? "chosen" : ""}`}>
    <Handle type="target" position={Position.Left} />
    <button className="overview-stage-heading nodrag" onClick={() => data.onStage(data.state)} aria-label={`Filter ${stateLabel(data.state)} tasks`}>
      <span><i />{stages.find((stage) => stage.id === data.state)?.short ?? data.state}</span><b>{number(data.count)}</b>
    </button>
    <div className="overview-node-tasks">
      {data.tasks.map((task) => <button key={task.runId} className="overview-task-card nodrag" onClick={() => data.onTask(task)} title={`${task.title} · ${taskDuration(task, data.now, true)}`} aria-label={`${task.incidentId}: ${task.title}`}>
        <span><b>{task.incidentId}</b><small>#{task.attemptNumber}</small><ArrowUpRight size={13} /></span>
        <span><small>{taskDuration(task, data.now, true)}</small><small>{task.usageRecorded ? number(task.tokensIn + task.tokensOut) : "—"}</small></span>
      </button>)}
      {!data.count && <div className="hud-node-idle" aria-label="No tasks"><i /><i /><i /></div>}
      {data.count > data.tasks.length && <button className="overview-more nodrag" onClick={() => data.onStage(data.state)}>+{number(data.count - data.tasks.length)} →</button>}
    </div>
    <Handle type="source" position={Position.Right} />
  </div>;
}
const nodeTypes = { stage: FlowStage };
const connections = [
  ["queued", "preparing_context", ""], ["preparing_context", "diagnosing", ""],
  ["diagnosing", "planning", "Code fix"], ["planning", "patching", ""], ["patching", "validating", ""],
  ["validating", "publishing", ""], ["publishing", "awaiting_human_review", "Repair ready"],
  ["diagnosing", "collecting_more_context", "More evidence"], ["collecting_more_context", "diagnosing", "Resume"],
  ["diagnosing", "completed_non_code", "Non-code"], ["planning", "diagnosis_ready_for_review", "Analysis only"],
  ["diagnosing", "blocked_manual_review", "Needs help"],
  ["validating", "failed", "Failure · any stage"], ["patching", "budget_exhausted", "Budget limit · any stage"],
];
export function TaskFlow({ overview, selected, onStage, onTask }: {
  overview: Overview; selected: string; onStage: (state: string) => void; onTask: (task: OverviewTask) => void;
}) {
  const nodes = useMemo<StageNode[]>(() => {
    const known = new Set<string>(stages.map((stage) => stage.id));
    const layout = [...stages, ...overview.stages.filter((stage) => !known.has(stage.state)).map((stage, index) => ({
      id: stage.state, label: stateLabel(stage.state), short: stage.state, x: index * 290, y: 700,
    }))];
    return layout.map((stage) => {
      const summary = overview.stages.find((item) => item.state === stage.id);
      return { id: stage.id, type: "stage", position: { x: stage.x, y: stage.y }, style: { pointerEvents: "all" },
        data: { state: stage.id, count: summary?.count ?? 0, tasks: summary?.tasks ?? [], now: Date.parse(overview.generatedAt),
          selected: selected === stage.id, onStage, onTask } };
    });
  }, [overview, selected, onStage, onTask]);
  const edges = useMemo<Edge[]>(() => connections.map(([source, target, label]) => ({
    id: `${source}-${target}`, source, target, label: undefined, ariaLabel: label || `${source} to ${target}`, type: "smoothstep",
    animated: !isTerminal(source) && overview.stages.some((stage) => stage.state === source && stage.count > 0),
    markerEnd: { type: MarkerType.ArrowClosed, color: "#176d52" },
    style: { stroke: "#176d52", strokeWidth: 1.5 },
  })), [overview.stages]);
  const focusStage = stages.find((stage) => !isTerminal(stage.id) && overview.stages.some((item) => item.state === stage.id && item.count > 0))?.id ?? "diagnosing";
  return <div className="overview-flow" aria-label="Live task workflow">
    <ReactFlow<StageNode> nodes={nodes} edges={edges} nodeTypes={nodeTypes} fitView fitViewOptions={{ nodes: [{ id: focusStage }], padding: 0.1, minZoom: 0.9, maxZoom: 1 }}
      nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} minZoom={0.3} maxZoom={1.5}
      preventScrolling={false} zoomOnScroll={false} panOnScroll={false}>
      <Background gap={24} color="#d6ded4" />
      <Controls showInteractive={false} />
    </ReactFlow>
  </div>;
}
