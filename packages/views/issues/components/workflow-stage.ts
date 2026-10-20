export interface WorkflowStage {
  label: string;
  next: string;
}

// The product workflow uses existing issue statuses, so older clients and
// custom workspace statuses remain compatible while the detail view can name
// the next responsible role.
export function workflowStage(status: string): WorkflowStage {
  switch (status) {
    case "todo":
      return { label: "等待 Mika 分配", next: "Mika" };
    case "in_progress":
      return { label: "代码执行中", next: "代码智能体" };
    case "in_review":
      return { label: "等待审核", next: "审核智能体" };
    case "done":
      return { label: "已完成", next: "Mika 汇报结果" };
    case "blocked":
      return { label: "需要处理阻塞", next: "Mika" };
    default:
      return { label: "进行中", next: "Mika" };
  }
}
