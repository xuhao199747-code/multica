"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { useT } from "../../i18n";
import {
  projectWorkflowSettingsOptions,
  projectAgentBindingsOptions,
  projectWorkflowKeys,
} from "@multica/core/projects/workflow-queries";

interface Props {
  sessionId: string;
  projectId: string;
  agentId: string;
  projectTitle: string;
}

function snapshotString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function snapshotArray(value: unknown) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => String(item ?? "").trim()).filter(Boolean);
}

export function ProjectWorkflowStartPanel({
  sessionId,
  projectId,
  agentId,
  projectTitle,
  messages,
}: Props & { messages: { role?: string; content?: unknown }[] }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const { data: settings } = useQuery(projectWorkflowSettingsOptions(wsId, projectId));
  const { data: bindings } = useQuery(projectAgentBindingsOptions(wsId, projectId));

  const canStart =
    !!settings?.enabled &&
    (bindings?.bindings ?? []).some(
      (binding) =>
        binding.agent_id === agentId && (binding.role === "mika" || binding.role === "prd"),
    );

  const start = useMutation({
    mutationFn: () => {
      const latestUserMessage = [...messages].reverse().find((message) => message.role === "user");
      const prompt = snapshotString(latestUserMessage?.content);
      const goal = snapshotString(prompt) || projectTitle.trim() || "Project development request";
      const scope = snapshotArray(latestUserMessage && prompt ? [prompt] : []);
      return api.startProjectWorkflowExecution(projectId, {
        source_chat_session_id: sessionId,
        source_agent_id: agentId,
        title: `${goal} (development)`,
        snapshot: {
          goal,
          scope,
          acceptance_criteria: snapshotArray([
            "The confirmed snapshot is implemented in the project.",
            "Relevant tests or checks are run and captured as evidence.",
          ]),
          risks: prompt ? [prompt] : [],
        },
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: [...projectWorkflowKeys.settings(wsId, projectId), "executions"],
      });
      toast.success(t(($) => $.workflow.toast_started));
    },
    onError: () => {
      toast.error(t(($) => $.workflow.toast_start_failed));
    },
  });

  if (!canStart) return null;

  return (
    <div className="border-t px-3 py-2">
      <Button
        variant="secondary"
        size="sm"
        className="w-full"
        disabled={start.isPending}
        onClick={() => start.mutate()}
      >
        {start.isPending ? t(($) => $.workflow.start_pending) : t(($) => $.workflow.start_button)}
      </Button>
    </div>
  );
}
