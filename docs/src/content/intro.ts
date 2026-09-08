export type IntroLocale = "en" | "zh-CN";

export interface IntroContent {
  lang: IntroLocale;
  title: string;
  description: string;
  eyebrow: string;
  summary: string;
  authority: string;
  previewLabel: string;
  primaryAction: string;
  primaryHref: string;
  secondaryAction: string;
  secondaryHref: string;
  mediaAlt: string;
  mediaCaption: string;
  sectionLabels: {
    workflow: string;
    evidence: string;
    authority: string;
    readiness: string;
  };
  workflowHeading: string;
  workflowIntro: string;
  workflow: Array<{ step: string; title: string; text: string }>;
  evidenceHeading: string;
  evidenceText: string;
  evidencePoints: string[];
  boundaryHeading: string;
  boundaryText: string;
  boundaries: Array<{ label: string; value: string }>;
  readinessHeading: string;
  readinessText: string;
  readinessAction: string;
}

export const introContent: Record<IntroLocale, IntroContent> = {
  en: {
    lang: "en",
    title: "FixThe | Production incident investigation",
    description:
      "FixThe is a customer-managed system for evidence-backed incident investigation and controlled remediation.",
    eyebrow: "Customer-managed incident operations",
    summary:
      "Investigate production incidents with bounded evidence, durable reasoning, and an explicit path to human review.",
    authority:
      "Deterministic operation does not require a remote LLM. Optional remote analysis receives redacted context; merge and production deployment remain human-controlled.",
    previewLabel: "Documentation preview",
    primaryAction: "Review readiness",
    primaryHref: "/docs/get-started/",
    secondaryAction: "Architecture",
    secondaryHref: "/docs/concepts/architecture/",
    mediaAlt:
      "FixThe incident detail showing evidence-backed diagnosis, candidate plans, and remediation review",
    mediaCaption:
      "Incident investigation and remediation review · Synthetic data",
    sectionLabels: {
      workflow: "Workflow",
      evidence: "Evidence",
      authority: "Authority",
      readiness: "Readiness",
    },
    workflowHeading: "From signal to review",
    workflowIntro:
      "Each transition is bounded, attributable, and inspectable by the operator.",
    workflow: [
      {
        step: "01",
        title: "Receive",
        text: "Normalize a trusted incident signal.",
      },
      {
        step: "02",
        title: "Investigate",
        text: "Collect scoped evidence and code context.",
      },
      {
        step: "03",
        title: "Review",
        text: "Present diagnosis and candidate plans.",
      },
      {
        step: "04",
        title: "Control",
        text: "Keep merge and deployment with people.",
      },
    ],
    evidenceHeading: "Evidence before confidence",
    evidenceText:
      "FixThe preserves where a claim came from and keeps observation separate from model interpretation.",
    evidencePoints: [
      "Project-scoped repository and runtime evidence",
      "Durable attempts, citations, and correction history",
      "Bounded tool access with secret-free review projections",
    ],
    boundaryHeading: "Authority stays visible",
    boundaryText:
      "Automation is useful only when its limits are explicit. FixThe keeps those limits in the product contract.",
    boundaries: [
      {
        label: "Runs locally",
        value: "Core incident handling and deterministic policy",
      },
      {
        label: "Optional remote step",
        value: "Redacted analysis through configured providers",
      },
      {
        label: "Always human",
        value: "Merge, deployment, and production recovery claims",
      },
    ],
    readinessHeading: "Evaluate the operating model",
    readinessText:
      "The public installation path is not released yet. The preview documents the current architecture and readiness gates without presenting planned capabilities as available.",
    readinessAction: "Open the documentation preview",
  },
  "zh-CN": {
    lang: "zh-CN",
    title: "FixThe | 生产事故调查",
    description:
      "FixThe 是一套由客户自行管理、以证据为基础的生产事故调查与受控修复系统。",
    eyebrow: "客户自行管理的事故响应",
    summary: "通过受限证据、可持久化推理和明确的人工审查路径调查生产事故。",
    authority:
      "确定性运行不依赖远程大模型。可选的远程分析只接收脱敏上下文；合并与生产部署始终由人控制。",
    previewLabel: "文档预览",
    primaryAction: "查看就绪状态",
    primaryHref: "/zh-cn/docs/get-started/",
    secondaryAction: "架构说明",
    secondaryHref: "/zh-cn/docs/concepts/architecture/",
    mediaAlt: "FixThe 事故详情，展示基于证据的诊断、候选方案和修复审查",
    mediaCaption: "事故调查与修复审查 · 合成演示数据",
    sectionLabels: {
      workflow: "工作流程",
      evidence: "调查证据",
      authority: "权限边界",
      readiness: "就绪状态",
    },
    workflowHeading: "从信号到审查",
    workflowIntro: "每一次状态转换都有边界、可归因，并且可由操作人员检查。",
    workflow: [
      { step: "01", title: "接收", text: "规范化可信的事故信号。" },
      { step: "02", title: "调查", text: "采集受限证据和代码上下文。" },
      { step: "03", title: "审查", text: "呈现诊断结果和候选方案。" },
      { step: "04", title: "控制", text: "由人保留合并和部署权限。" },
    ],
    evidenceHeading: "先有证据，再谈置信度",
    evidenceText: "FixThe 保留每项判断的来源，并将客观观察与模型解释分开。",
    evidencePoints: [
      "项目范围内的代码仓库与运行时证据",
      "可持久化的尝试记录、引用与纠正历史",
      "受限工具权限和不含秘密的审查视图",
    ],
    boundaryHeading: "权限边界始终可见",
    boundaryText:
      "只有边界明确，自动化才真正有用。FixThe 将这些限制写入产品契约。",
    boundaries: [
      { label: "本地运行", value: "核心事故处理与确定性策略" },
      { label: "可选远程步骤", value: "通过已配置服务进行脱敏分析" },
      { label: "始终由人负责", value: "合并、部署与生产恢复结论" },
    ],
    readinessHeading: "评估运行模式",
    readinessText:
      "公开安装路径尚未发布。本预览仅说明当前架构和就绪门禁，不会把规划中的能力描述为已经可用。",
    readinessAction: "打开文档预览",
  },
};
