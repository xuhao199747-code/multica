"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import {
  projectWorkflowSettingsOptions,
  projectExecutionsOptions,
  projectAgentBindingsOptions,
  useCreateProjectAgentBinding,
  useDeleteProjectAgentBinding,
  useUpdateProjectWorkflowSettings,
} from "@multica/core/projects/workflow-queries";
import { ProjectSquadTemplateSection } from "./project-squad-template-section";
import type { ProjectAgentRole, ProjectDeliveryMode } from "@multica/core/types";
import { Switch } from "@multica/ui/components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";

const deliveryModes: ProjectDeliveryMode[] = [
  "notify_only",
  "pull_request",
  "manual_release",
  "automated_release",
];
const roles: ProjectAgentRole[] = ["mika", "prd", "developer", "reviewer"];

function evidenceText(value: unknown) {
  return typeof value === "string" ? value : "";
}

function nestedEvidence(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function ProjectWorkflowSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: settings } = useQuery(projectWorkflowSettingsOptions(wsId, projectId));
  const { data: executions } = useQuery(projectExecutionsOptions(wsId, projectId));
  const { data: bindings } = useQuery(projectAgentBindingsOptions(wsId, projectId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const update = useUpdateProjectWorkflowSettings(wsId, projectId);
  const createBinding = useCreateProjectAgentBinding(wsId, projectId);
  const deleteBinding = useDeleteProjectAgentBinding(wsId, projectId);
  const router = useNavigation();
  const paths = useWorkspacePaths();
  const openMika = useMutation({
    mutationFn: () => api.openProjectMikaSession(projectId),
    onSuccess: (session) => router.push(paths.chatSession(session.id)),
  });
  const workflowPhase = (state: string, reviewRound: number) => {
    switch (state) {
      case "development":
        return t(($) => $.workflow.phase.development);
      case "review":
        return t(($) => $.workflow.phase.review, { round: reviewRound });
      case "repair":
        return t(($) => $.workflow.phase.repair);
      case "delivering":
        return t(($) => $.workflow.phase.delivering);
      case "completed":
        return t(($) => $.workflow.phase.completed);
      case "needs_member_decision":
        return t(($) => $.workflow.phase.needs_member_decision);
      default:
        return state;
    }
  };
  if (!settings) return null;

  const save = (patch: Partial<typeof settings>) => {
    update.mutate({
      enabled: settings.enabled,
      project_context: settings.project_context,
      delivery_mode: settings.delivery_mode,
      delivery_config: settings.delivery_config,
      ...patch,
    });
  };

  return (
    <section className="space-y-2 border-t pt-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-body font-medium">{t(($) => $.workflow.title)}</div>
          <p className="text-caption text-muted-foreground">{t(($) => $.workflow.description)}</p>
        </div>
        <Switch
          checked={settings.enabled}
          disabled={update.isPending}
          onCheckedChange={(enabled) => save({ enabled })}
          aria-label={t(($) => $.workflow.toggle_label)}
        />
      </div>
      {settings.enabled && (
        <div className="space-y-2">
        <label className="block space-y-1.5">
          <span className="text-caption text-muted-foreground">{t(($) => $.workflow.delivery_label)}</span>
          <Select
            items={deliveryModes.map((mode) => ({
              value: mode,
              label: t(($) => $.workflow.delivery_modes[mode]),
            }))}
            value={settings.delivery_mode}
            onValueChange={(value) => save({ delivery_mode: value as ProjectDeliveryMode })}
          >
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              {deliveryModes.map((mode) => <SelectItem key={mode} value={mode}>{t(($) => $.workflow.delivery_modes[mode])}</SelectItem>)}
            </SelectContent>
          </Select>
        </label>
        <label className="block space-y-1.5">
          <span className="text-caption text-muted-foreground">{t(($) => $.workflow.delivery_instructions_label)}</span>
          <Textarea
            key={`${settings.updated_at ?? ""}:${String(settings.delivery_config.instructions ?? "")}`}
            defaultValue={typeof settings.delivery_config.instructions === "string" ? settings.delivery_config.instructions : ""}
            placeholder={t(($) => $.workflow.delivery_instructions_placeholder)}
            onBlur={(event) => save({ delivery_config: { ...settings.delivery_config, instructions: event.currentTarget.value.trim() } })}
            rows={2}
          />
        </label>
        <Button variant="secondary" size="sm" className="w-full" disabled={openMika.isPending} onClick={() => openMika.mutate()}>
          {t(($) => $.workflow.open_mica)}
        </Button>
        </div>
      )}
      {settings.enabled && executions && executions.total > 0 && (
        <div className="space-y-1 border-t pt-2 text-caption">
          {executions.executions.slice(0, 3).map((execution) => (
            <div key={execution.id} className="space-y-1 rounded-md bg-surface-raised px-2 py-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="truncate">{execution.main_issue?.title ?? String(execution.snapshot.goal ?? t(($) => $.workflow.execution_fallback))}</span>
                <span className="shrink-0 text-muted-foreground">{workflowPhase(execution.state, execution.review_round)}</span>
              </div>
              {(evidenceText(execution.delivery_result.commit) || evidenceText(execution.delivery_result.tests) || evidenceText(nestedEvidence(execution.delivery_result.review).findings)) && (
                <div className="space-y-0.5 text-muted-foreground">
                  {evidenceText(execution.delivery_result.commit) && <div>{t(($) => $.workflow.evidence_commit)}: {evidenceText(execution.delivery_result.commit)}</div>}
                  {evidenceText(execution.delivery_result.tests) && <div>{t(($) => $.workflow.evidence_tests)}: {evidenceText(execution.delivery_result.tests)}</div>}
                  {evidenceText(nestedEvidence(execution.delivery_result.review).findings) && <div>{t(($) => $.workflow.evidence_review)}: {evidenceText(nestedEvidence(execution.delivery_result.review).findings)}</div>}
                </div>
              )}
              {execution.historical_issues.length > 0 && (
                <details className="text-muted-foreground">
                  <summary className="cursor-pointer">{t(($) => $.workflow.historical_issues, { count: execution.historical_issues.length })}</summary>
                  <div className="mt-1 space-y-0.5 pl-2">
                    {execution.historical_issues.map((issue) => <div key={issue.id} className="truncate">{issue.title}</div>)}
                  </div>
                </details>
              )}
            </div>
          ))}
        </div>
      )}
      {settings.enabled && (
        <ProjectSquadTemplateSection projectId={projectId} />
      )}
      {settings.enabled && bindings && (
        <div className="space-y-2">
          <span className="text-caption text-muted-foreground">{t(($) => $.workflow.roles_label)}</span>
          {roles.map((role) => {
            const binding = bindings.bindings.find((item) => item.role === role);
            const source = binding?.source ?? "manual";
            return <div key={role} className="flex items-center gap-2">
              <span className="w-20 shrink-0 text-caption">{t(($) => $.workflow.roles[role])}</span>
              <Select
                items={agents.filter((agent) => !agent.archived_at).map((agent) => ({
                  value: agent.id,
                  label: agent.name,
                }))}
                value={binding?.agent_id ?? ""}
                onValueChange={async (agentId) => {
                  if (!agentId) return;
                  if (binding && binding.agent_id !== agentId) await deleteBinding.mutateAsync(binding.id);
                  createBinding.mutate({ agent_id: agentId, role });
                }}
              >
                <SelectTrigger className="h-8 flex-1"><SelectValue placeholder={t(($) => $.workflow.choose_agent)} /></SelectTrigger>
                <SelectContent>{agents.filter((agent) => !agent.archived_at).map((agent) => <SelectItem key={agent.id} value={agent.id}>{agent.name}</SelectItem>)}</SelectContent>
              </Select>
              {binding && (
                <span
                  title={t(($) => (source === "squad" ? $.workflow.squad.role_source_squad : $.workflow.squad.role_source_manual))}
                  className={
                    source === "squad"
                      ? "shrink-0 rounded-xs bg-accent px-1.5 py-0.5 text-caption text-muted-foreground"
                      : "shrink-0 rounded-xs bg-surface-raised px-1.5 py-0.5 text-caption text-muted-foreground"
                  }
                >
                  {t(($) => (source === "squad" ? $.workflow.squad.role_source_squad : $.workflow.squad.role_source_manual))}
                </span>
              )}
            </div>;
          })}
        </div>
      )}
    </section>
  );
}
