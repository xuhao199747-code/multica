"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ExternalLink } from "lucide-react";
import { api } from "@multica/core/api";
import {
  extractProjectWorkflowPlanConfirmation,
} from "@multica/core/chat";
import {
  projectAgentBindingsOptions,
  projectExecutionsOptions,
  projectWorkflowKeys,
  projectWorkflowSettingsOptions,
} from "@multica/core/projects/workflow-queries";
import type { ProjectExecution } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

interface ProjectWorkflowPlanConfirmationCardProps {
  workspaceId: string;
  projectId: string;
  sessionId: string;
  agentId: string;
  messageId: string;
  content: string;
  issueHref: (issueId: string) => string;
}

function isActiveExecution(execution: ProjectExecution): boolean {
  return execution.state !== "completed" && execution.state !== "cancelled";
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim()
    ? error.message
    : fallback;
}

/**
 * A project-workflow-only confirmation affordance. It deliberately binds the
 * request to this persisted assistant message and its parsed plan marker.
 */
export function ProjectWorkflowPlanConfirmationCard({
  workspaceId,
  projectId,
  sessionId,
  agentId,
  messageId,
  content,
  issueHref,
}: ProjectWorkflowPlanConfirmationCardProps) {
  const { t } = useT("projects");
  const queryClient = useQueryClient();
  const [started, setStarted] = useState<ProjectExecution | null>(null);
  const confirmation = useMemo(
    () => extractProjectWorkflowPlanConfirmation(content),
    [content],
  );
  const enabled = confirmation !== null;
  const { data: settings, isSuccess: settingsLoaded } = useQuery({
    ...projectWorkflowSettingsOptions(workspaceId, projectId),
    enabled,
  });
  const { data: bindings, isSuccess: bindingsLoaded } = useQuery({
    ...projectAgentBindingsOptions(workspaceId, projectId),
    enabled,
  });
  const { data: executions, isSuccess: executionsLoaded } = useQuery({
    ...projectExecutionsOptions(workspaceId, projectId),
    enabled,
  });

  const canConfirm =
    !!settings?.enabled
    && (bindings?.bindings ?? []).some(
      (binding) =>
        binding.agent_id === agentId
        && (binding.role === "mika" || binding.role === "prd"),
    );
  const activeExecution = (executions?.executions ?? []).find(
    (execution) =>
      execution.source_chat_session_id === sessionId && isActiveExecution(execution),
  );
  const execution = started ?? activeExecution ?? null;
  const start = useMutation({
    mutationFn: () => {
      if (!confirmation) throw new Error("This plan can no longer be confirmed.");
      return api.startProjectWorkflowExecution(projectId, {
        source_chat_session_id: sessionId,
        source_agent_id: agentId,
        source_message_id: messageId,
        snapshot: confirmation.snapshot,
      });
    },
    onSuccess: (result) => {
      setStarted(result.execution);
      queryClient.invalidateQueries({
        queryKey: [...projectWorkflowKeys.settings(workspaceId, projectId), "executions"],
      });
    },
  });

  if (!confirmation || !settingsLoaded || !bindingsLoaded || !executionsLoaded || !canConfirm) return null;

  if (execution) {
    const issue = execution.main_issue;
    return (
      <div className="mt-3 rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2.5 text-body">
        <div className="flex items-center gap-2 text-emerald-700 dark:text-emerald-300">
          <CheckCircle2 className="size-4" aria-hidden="true" />
          <span>{started ? t(($) => $.workflow.execution_started) : t(($) => $.workflow.execution_active)}</span>
        </div>
        {issue ? (
          <AppLink
            href={issueHref(issue.id)}
            newTabTitle={issue.title}
            className="mt-1.5 inline-flex items-center gap-1 text-caption font-medium text-foreground underline-offset-4 hover:underline"
          >
            {issue.title}
            <ExternalLink className="size-3" aria-hidden="true" />
          </AppLink>
        ) : null}
      </div>
    );
  }

  const retry = start.isError;
  return (
    <div className="mt-3 rounded-md border bg-muted/30 px-3 py-2.5">
      {start.isError ? (
        <p role="alert" className="mb-2 text-caption text-destructive">
          {errorMessage(start.error, t(($) => $.workflow.toast_start_failed))}
        </p>
      ) : null}
      <Button
        type="button"
        size="sm"
        variant="secondary"
        disabled={start.isPending}
        onClick={() => start.mutate()}
      >
        {start.isPending
          ? t(($) => $.workflow.start_pending)
          : retry
            ? t(($) => $.workflow.start_retry)
            : t(($) => $.workflow.start_button)}
      </Button>
    </div>
  );
}
