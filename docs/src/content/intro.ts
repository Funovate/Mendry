export type IntroLocale = "en" | "zh-CN";

type IntroPath = { title: string; text: string; href: string };

export interface IntroContent {
  lang: IntroLocale;
  title: string;
  description: string;
  heading: [string, string];
  summary: string;
  primaryAction: string;
  primaryHref: string;
  secondaryAction: string;
  secondaryHref: string;
  statusLabel: string;
  statusHref: string;
  demo: {
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
    delivery: Array<{ title: string; text: string }>;
    note: string;
    traits: string[];
  };
  workflow: {
    heading: string;
    text: string;
    flows: Array<{
      id: "investigation" | "hotfix";
      label: string;
      badge: string;
      stages: Array<{ title: string; text: string }>;
    }>;
    gateLabel: string;
    handoff: { heading: string; stages: string[]; text: string };
    policy: string;
  };
  workspace: {
    heading: string;
    text: string;
    paths: IntroPath[];
  };
  integration: {
    heading: string;
    text: string;
    action: string;
    href: string;
    sources: string[];
    hub: string;
    deliveryLabel: string;
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
    title: "Self-Hosted AI Incident Investigation & Hotfix",
    description:
      "Connect production alerts, logs, and code. Investigate root causes and deliver policy-constrained fixes as review branches or draft pull requests, with optional local prevalidation.",
    heading: ["From production alert", "to a fix ready for review."],
    summary:
      "Mendry connects alerts, logs, and code to investigate root causes. Enable automatic hotfix to generate constrained patches, optionally test them locally, and deliver the changes for your team to review.",
    primaryAction: "Start evaluating",
    primaryHref: "/docs/get-started/",
    secondaryAction: "Explore the workflow",
    secondaryHref: "#workflow",
    statusLabel: "Source preview · Product status",
    statusHref: "/docs/project/status/",
    demo: {
      example: "ILLUSTRATIVE EXAMPLE",
      incident: "Checkout requests are failing",
      source: "Production signal / checkout-api",
      stages: ["01 / Signal", "02 / Investigation", "03 / Patch"],
      signalTitle: "Checkout errors increase.",
      signalText:
        "A production alert and runtime logs point to the checkout service.",
      evidenceTitle: "The code accesses a missing session.",
      evidenceText:
        "Correlate the exception with repository context and retain the evidence behind the diagnosis.",
      proposalTitle: "Guard the expired-session path.",
      proposalText:
        "Automatic hotfix generates a candidate patch within the project's allowed paths and change limits.",
      suggestion: "CANDIDATE DIFF",
      delivery: [
        {
          title: "Local prevalidation",
          text: "Optional · isolated container tests",
        },
        { title: "Review delivery", text: "Review branch / Draft PR or MR" },
        {
          title: "Result notification",
          text: "Telegram, Feishu, or WeCom",
        },
        { title: "Team review", text: "Repository CI, merge & deployment" },
      ],
      note: "Illustrative automatic-hotfix flow, not a live run. Local prevalidation is optional; delivery depends on repository support and credentials.",
      traits: [
        "Evidence-linked diagnosis",
        "Policy-constrained patches",
        "Reviewable Git delivery",
      ],
    },
    workflow: {
      heading: "Investigate. Repair. Hand off.",
      text: "Start with analysis only. Enable automatic hotfix per project to carry a diagnosis through to a review branch or draft change request.",
      flows: [
        {
          id: "investigation",
          label: "INVESTIGATION · DEFAULT",
          badge: "Analysis only",
          stages: [
            {
              title: "Collect signals",
              text: "Ingest alerts and runtime observations in the context of a project.",
            },
            {
              title: "Investigate root cause",
              text: "Connect logs, stack traces, and the deployed code baseline.",
            },
            {
              title: "Review findings",
              text: "Inspect the diagnosis, evidence, recommended plan, and candidate diff.",
            },
          ],
        },
        {
          id: "hotfix",
          label: "AUTOMATIC HOTFIX · OPT-IN",
          badge: "Available in preview",
          stages: [
            {
              title: "Generate a bounded patch",
              text: "Restrict edits by allowed paths, protected files, and change size.",
            },
            {
              title: "Prevalidate locally · optional",
              text: "With enhanced validation configured, run baseline and patched tests in a network-isolated container.",
            },
            {
              title: "Deliver for review",
              text: "Push a review branch and create a Draft PR/MR where the repository adapter supports it.",
            },
          ],
        },
      ],
      gateLabel: "Enable per project · Configure policy & write credentials",
      handoff: {
        heading: "Your team takes it from here",
        stages: [
          "Repository CI",
          "Review & merge",
          "Deploy",
          "Confirm recovery",
        ],
        text: "Mendry does not automatically merge, deploy, roll back, or confirm production recovery. Operators update the incident status after checking the result.",
      },
      policy:
        "Analysis only is the default. AWS-source incidents remain analysis only.",
    },
    workspace: {
      heading: "One workspace for the incident",
      text: "Follow incoming signals, prioritize open incidents, and inspect each investigation and repair attempt.",
      paths: [
        {
          title: "Observation stream",
          text: "Browse incoming observations and filter the loaded page by severity or keywords.",
          href: "/docs/guides/operator-workflow/",
        },
        {
          title: "Incidents",
          text: "Browse incidents by status, then open one to see its remediation stage and next step.",
          href: "/docs/guides/operator-workflow/",
        },
        {
          title: "Incident details",
          text: "Review evidence, candidate diffs, attempt history, recovery information, and hotfix delivery.",
          href: "/docs/reference/features/",
        },
      ],
    },
    integration: {
      heading: "Connect evidence to delivery",
      text: "Bring signed webhooks, supported log sources, and a Git baseline into the same workflow. Configure repair policy and repository access for each project.",
      action: "Explore automatic hotfix",
      href: "/docs/guides/automatic-hotfix/",
      sources: ["Alerts", "Logs", "Git baseline"],
      hub: "Investigate → patch → review",
      deliveryLabel: "DELIVERY FOR YOUR TEAM",
      targets: ["Review branch", "Draft PR / MR"],
      traits: [
        {
          title: "Self-hosted",
          text: "Run the service and configure its evidence sources in your own environment.",
        },
        {
          title: "Bounded changes",
          text: "Define where repairs can make changes and how large a patch may be.",
        },
        {
          title: "Traceable attempts",
          text: "Inspect execution records, validation results, and delivery details in the incident.",
        },
      ],
    },
    start: {
      heading: "Choose your next step",
      paths: [
        {
          title: "Evaluate Mendry",
          text: "Prepare an environment, configure a project, and investigate your first incident.",
          href: "/docs/get-started/",
        },
        {
          title: "Set up automatic hotfix",
          text: "Configure repair policy, repository delivery, and optional local prevalidation.",
          href: "/docs/guides/automatic-hotfix/",
        },
        {
          title: "Configure project notifications",
          text: "Send incident triggers and the first AI result to Telegram, Feishu, or WeCom.",
          href: "/docs/guides/notifications/",
        },
      ],
    },
    footer: {
      tagline: "From production signal to a reviewable fix.",
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
    title: "自托管 AI 生产问题调查与自动修复",
    description:
      "连接生产告警、日志与代码，调查问题根因，在策略约束下生成修复补丁，通过评审分支或草稿 PR 交付，并可选执行本地预验证。",
    heading: ["从生产告警，", "到可审查的修复。"],
    summary:
      "Mendry 关联告警、日志与代码，定位问题根因。启用自动 Hotfix 后，可在策略约束下生成补丁、按需执行本地测试，并将修复交付给团队审查。",
    primaryAction: "开始评估",
    primaryHref: "/zh-cn/docs/get-started/",
    secondaryAction: "了解处理流程",
    secondaryHref: "#workflow",
    statusLabel: "源码预览 · 查看产品状态",
    statusHref: "/zh-cn/docs/project/status/",
    demo: {
      example: "流程示例",
      incident: "结算请求持续出现异常",
      source: "生产信号 / checkout-api",
      stages: ["01 / 发现信号", "02 / 调查根因", "03 / 生成补丁"],
      signalTitle: "结算接口错误增加。",
      signalText: "生产告警与运行时日志将调查范围指向结算服务。",
      evidenceTitle: "代码访问了空会话。",
      evidenceText: "结合异常与仓库上下文定位问题，保留支撑诊断结论的证据。",
      proposalTitle: "补充会话空值检查。",
      proposalText: "自动 Hotfix 在项目允许的路径和变更规模内生成候选补丁。",
      suggestion: "候选 DIFF",
      delivery: [
        { title: "本地预验证", text: "可选 · 隔离容器测试" },
        { title: "交付审查", text: "评审分支 / 草稿 PR 或 MR" },
        { title: "结果通知", text: "Telegram、飞书或企业微信" },
        { title: "团队接续", text: "仓库 CI、审查合并与部署" },
      ],
      note: "此处为自动 Hotfix 流程示例，非真实运行结果。本地预验证可选，交付方式取决于仓库支持与凭据配置。",
      traits: ["诊断关联证据", "补丁受策略约束", "Git 交付可审查"],
    },
    workflow: {
      heading: "从调查到修复交付",
      text: "默认仅分析。按项目启用自动 Hotfix 后，可将诊断推进到受限补丁与评审分支，或交付草稿 PR / MR。",
      flows: [
        {
          id: "investigation",
          label: "问题调查 · 默认启用",
          badge: "仅分析模式",
          stages: [
            {
              title: "汇聚生产信号",
              text: "按项目接入告警与运行时观测，建立事故上下文。",
            },
            {
              title: "定位问题根因",
              text: "关联日志、异常堆栈与已部署代码基线，形成诊断。",
            },
            {
              title: "审查调查结果",
              text: "查看诊断结论、证据引用、推荐方案与候选 Diff。",
            },
          ],
        },
        {
          id: "hotfix",
          label: "自动 HOTFIX · 按项目启用",
          badge: "当前预览能力",
          stages: [
            {
              title: "生成受限补丁",
              text: "按允许路径、保护文件和变更规模限制修复范围。",
            },
            {
              title: "本地预验证 · 可选",
              text: "配置增强验证后，在隔离网络的容器中运行基线与补丁后的测试。",
            },
            {
              title: "交付代码审查",
              text: "推送独立评审分支，并在仓库适配器支持时创建草稿 PR / MR。",
            },
          ],
        },
      ],
      gateLabel: "按项目启用 · 配置变更策略与写凭据",
      handoff: {
        heading: "由你的团队接续",
        stages: ["仓库 CI", "审查与合并", "部署上线", "确认恢复"],
        text: "Mendry 不自动合并、部署、回滚或确认生产恢复。操作员核查处理结果后，更新事故状态。",
      },
      policy: "默认仅分析；AWS 来源事故仍保持仅分析。",
    },
    workspace: {
      heading: "在同一工作台跟进事故",
      text: "从观测信号进入调查，按处理阶段安排优先级，查看每次调查与修复尝试。",
      paths: [
        {
          title: "事件流",
          text: "浏览接入的观测信号，按等级或关键词筛选当前已加载页。",
          href: "/zh-cn/docs/guides/operator-workflow/",
        },
        {
          title: "事故列表",
          text: "按状态浏览事故，进入事故查看修复阶段与下一步操作。",
          href: "/zh-cn/docs/guides/operator-workflow/",
        },
        {
          title: "事故详情",
          text: "检查证据、候选 Diff、尝试记录、恢复信息与 Hotfix 交付结果。",
          href: "/zh-cn/docs/reference/features/",
        },
      ],
    },
    integration: {
      heading: "连接证据与修复交付",
      text: "将签名 Webhook、受支持的日志来源与 Git 基线接入同一流程，按项目配置修复策略和仓库访问。",
      action: "了解自动 Hotfix",
      href: "/zh-cn/docs/guides/automatic-hotfix/",
      sources: ["告警信号", "日志", "Git 基线"],
      hub: "调查 → 补丁 → 审查",
      deliveryLabel: "交付团队审查",
      targets: ["评审分支", "草稿 PR / MR"],
      traits: [
        {
          title: "自托管运行",
          text: "在自己的环境部署服务，配置调查所需的证据来源。",
        },
        {
          title: "变更范围可控",
          text: "明确允许修改的路径，以及单次补丁的变更规模。",
        },
        {
          title: "修复尝试可追溯",
          text: "在事故中查看执行记录、验证结果和交付详情。",
        },
      ],
    },
    start: {
      heading: "开始使用",
      paths: [
        {
          title: "开始评估",
          text: "准备环境、配置项目，调查首个生产事故。",
          href: "/zh-cn/docs/get-started/",
        },
        {
          title: "配置自动 Hotfix",
          text: "设置修复策略、仓库交付与可选的本地预验证。",
          href: "/zh-cn/docs/guides/automatic-hotfix/",
        },
        {
          title: "配置项目通知",
          text: "将事故触发与首次 AI 处理结果发送到 Telegram、飞书或企业微信。",
          href: "/zh-cn/docs/guides/notifications/",
        },
      ],
    },
    footer: {
      tagline: "从生产信号，到可审查的修复。",
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
