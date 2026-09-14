export type IntroLocale = "en" | "zh-CN";

type IntroPath = { title: string; text: string; href: string };

export interface IntroContent {
  lang: IntroLocale;
  title: string;
  description: string;
  eyebrow: string;
  heading: [string, string];
  summary: string;
  primaryAction: string;
  primaryHref: string;
  secondaryAction: string;
  secondaryHref: string;
  statusLabel: string;
  statusHref: string;
  demo: {
    heading: string;
    example: string;
    incident: string;
    source: string;
    stages: [string, string, string];
    signalTitle: string;
    signalText: string;
    evidenceTitle: string;
    evidenceText: string;
    proposalTitle: string;
    proposalText: string;
    suggestion: string;
    note: string;
    traits: string[];
  };
  workflow: {
    heading: string;
    text: string;
    current: string;
    future: string;
    currentStages: string[];
    futureStages: string[];
    recovery: string;
    retry: string;
    policy: string;
  };
  integration: {
    heading: string;
    text: string;
    action: string;
    href: string;
    sources: string[];
    hub: string;
    future: string;
    targets: string[];
    traits: Array<{ title: string; text: string }>;
  };
  start: {
    heading: string;
    paths: IntroPath[];
  };
  footer: {
    tagline: string;
    navigation: string;
    links: Array<{ label: string; href: string }>;
    note: string;
    back: string;
  };
}

export const introContent: Record<IntroLocale, IntroContent> = {
  en: {
    lang: "en",
    title: "Self-Hosted AI Incident Investigation",
    description:
      "Investigate production incidents with alerts, logs, and code. Mendry turns traceable evidence into reviewable root cause findings and repair proposals.",
    eyebrow: "SELF-HOSTED · AI INCIDENT RESPONSE",
    heading: ["AI incident investigation.", "From alert to repair proposal."],
    summary:
      "Mendry connects production alerts, logs, and code to investigate root causes and generate repair proposals you can review.",
    primaryAction: "Start evaluating",
    primaryHref: "/docs/get-started/",
    secondaryAction: "Explore the workflow",
    secondaryHref: "#workflow",
    statusLabel: "Source preview · See product status",
    statusHref: "/docs/project/status/",
    demo: {
      heading: "From alert to repair proposal",
      example: "ILLUSTRATIVE EXAMPLE",
      incident: "Checkout requests are failing",
      source: "Production signal / checkout-api",
      stages: ["Signal", "Evidence", "Proposal"],
      signalTitle: "Checkout requests are failing.",
      signalText:
        "A rise in failed requests points the investigation to the checkout service.",
      evidenceTitle: "The code accesses a missing session.",
      evidenceText:
        "The exception and code context suggest a missing check for an expired session.",
      proposalTitle: "Add a session null check.",
      proposalText:
        "Guard against a missing session, then verify the expired-session path with a regression test.",
      suggestion: "SUGGESTED CHANGE",
      note: "Example investigation and proposed change. No patch, test, or deployment has been executed.",
      traits: [
        "Source-linked evidence",
        "Code-aware investigation",
        "Reviewable proposals",
      ],
    },
    workflow: {
      heading: "How it works",
      text: "Today, investigate and review a solution. Next, connect AI execution and CI/CD to carry it through to recovery.",
      current: "CURRENT SCOPE · PREVIEW",
      future: "EVOLUTION · PLANNED",
      currentStages: ["Detect", "Investigate", "Propose"],
      futureStages: ["Repair", "Test", "Git push / PR", "Deploy"],
      recovery: "Verify recovery & close",
      retry: "Not recovered? Return to investigation.",
      policy: "Direction: policy-defined automation and approval points.",
    },
    integration: {
      heading: "Connect your tools",
      text: "Bring monitoring signals, logs, and repository context into one investigation in your own environment.",
      action: "Explore integrations",
      href: "/docs/guides/signed-webhooks/",
      sources: ["Monitoring", "Logs", "Code"],
      hub: "Context → investigation → proposal",
      future: "PLANNED EXECUTION CONNECTIONS",
      targets: ["Git", "CI/CD", "Deployment"],
      traits: [
        {
          title: "Self-hosted",
          text: "Run investigations in your own environment.",
        },
        {
          title: "Scoped access",
          text: "Configure the projects, sources, and tools in scope.",
        },
        {
          title: "Traceable findings",
          text: "Keep the evidence behind every proposal within reach.",
        },
      ],
    },
    start: {
      heading: "Get started",
      paths: [
        {
          title: "Evaluate Mendry",
          text: "Environment, project setup, and your first incident",
          href: "/docs/get-started/",
        },
        {
          title: "Connect monitoring",
          text: "Bring trusted signals into the investigation",
          href: "/docs/guides/signed-webhooks/",
        },
        {
          title: "Understand the architecture",
          text: "The investigation system and its extension points",
          href: "/docs/concepts/architecture/",
        },
      ],
    },
    footer: {
      tagline: "From the first alert. Toward the final fix.",
      navigation: "Footer navigation",
      links: [
        { label: "Documentation", href: "/docs/" },
        { label: "Agent Harness", href: "/docs/concepts/agent-harness/" },
        { label: "Product status", href: "/docs/project/status/" },
      ],
      note: "Source evaluation preview",
      back: "Back to top",
    },
  },
  "zh-CN": {
    lang: "zh-CN",
    title: "自托管 AI 生产问题调查与修复",
    description:
      "连接生产告警、日志与代码，调查问题根因，并将可追溯证据转化为可审查的修复方案。",
    eyebrow: "自托管 · AI 生产问题排查",
    heading: ["从生产告警，", "定位问题根因。"],
    summary:
      "Mendry 连接告警、日志与代码，关联调查证据，生成可供审查的修复方案。",
    primaryAction: "开始评估",
    primaryHref: "/zh-cn/docs/get-started/",
    secondaryAction: "了解处理流程",
    secondaryHref: "#workflow",
    statusLabel: "源码预览 · 查看产品状态",
    statusHref: "/zh-cn/docs/project/status/",
    demo: {
      heading: "从告警到修复方案",
      example: "调查示例",
      incident: "结算请求持续出现异常",
      source: "生产信号 / checkout-api",
      stages: ["发现信号", "关联证据", "生成方案"],
      signalTitle: "结算接口错误增加。",
      signalText: "请求错误增加，将调查范围指向结算服务。",
      evidenceTitle: "代码访问了空会话。",
      evidenceText: "异常与代码上下文提示：会话过期后，缺少空值检查。",
      proposalTitle: "建议补充会话空值检查。",
      proposalText: "补充会话空值检查，并通过回归测试验证过期会话路径。",
      suggestion: "建议修改",
      note: "此处为调查与建议修改示例，尚未执行代码修复、测试或部署。",
      traits: ["证据关联来源", "结合代码调查", "方案可供审查"],
    },
    workflow: {
      heading: "处理流程",
      text: "当前聚焦调查与方案审查，后续通过 AI 执行与 CI/CD，将修复推进到生产恢复。",
      current: "当前覆盖 · 预览",
      future: "演进方向 · 规划中",
      currentStages: ["发现问题", "调查根因", "生成方案"],
      futureStages: ["执行修复", "测试验证", "Git 推送 / PR", "部署上线"],
      recovery: "确认恢复，关闭问题",
      retry: "未恢复？回到调查，继续修复。",
      policy: "目标设计：按策略配置自动执行范围与审批节点。",
    },
    integration: {
      heading: "接入现有工具",
      text: "将监控信号、日志和代码上下文带入同一次调查，在自己的环境里组织问题处理。",
      action: "查看接入文档",
      href: "/zh-cn/docs/guides/signed-webhooks/",
      sources: ["监控信号", "日志", "代码"],
      hub: "上下文 → 调查 → 方案",
      future: "执行侧扩展 · 规划中",
      targets: ["Git", "CI/CD", "部署环境"],
      traits: [
        { title: "自托管运行", text: "在自己的环境中组织调查。" },
        { title: "访问范围可配置", text: "按项目、来源和工具限定调查范围。" },
        { title: "调查记录可追溯", text: "保留证据与修复建议之间的联系。" },
      ],
    },
    start: {
      heading: "开始使用",
      paths: [
        {
          title: "开始评估",
          text: "准备环境、配置项目、排查首个事故",
          href: "/zh-cn/docs/get-started/",
        },
        {
          title: "接入监控",
          text: "将可信的生产信号带入调查",
          href: "/zh-cn/docs/guides/signed-webhooks/",
        },
        {
          title: "了解架构",
          text: "了解调查系统及其扩展机制",
          href: "/zh-cn/docs/concepts/architecture/",
        },
      ],
    },
    footer: {
      tagline: "从生产告警，走向修复闭环。",
      navigation: "页脚导航",
      links: [
        { label: "操作文档", href: "/zh-cn/docs/" },
        { label: "Agent Harness", href: "/zh-cn/docs/concepts/agent-harness/" },
        { label: "产品状态", href: "/zh-cn/docs/project/status/" },
      ],
      note: "源码评估预览",
      back: "回到顶部",
    },
  },
};
