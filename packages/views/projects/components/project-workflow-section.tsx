"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  projectWorkflowSettingsOptions,
  projectExecutionsOptions,
  useUpdateProjectWorkflowSettings,
} from "@multica/core/projects/workflow-queries";
import type { ProjectDeliveryMode } from "@multica/core/types";
import { Switch } from "@multica/ui/components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";

const deliveryModes: ProjectDeliveryMode[] = [
  "notify_only",
  "pull_request",
  "manual_release",
  "automated_release",
];

export function ProjectWorkflowSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const { data: settings } = useQuery(projectWorkflowSettingsOptions(wsId, projectId));
  const { data: executions } = useQuery(projectExecutionsOptions(wsId, projectId));
  const update = useUpdateProjectWorkflowSettings(wsId, projectId);
  const router = useNavigation();
  const paths = useWorkspacePaths();
  const openMika = useMutation({
    mutationFn: () => api.openProjectMikaSession(projectId),
    onSuccess: (session) => router.push(paths.chatSession(session.id)),
  });
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
          <Select value={settings.delivery_mode} onValueChange={(value) => save({ delivery_mode: value as ProjectDeliveryMode })}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              {deliveryModes.map((mode) => <SelectItem key={mode} value={mode}>{t(($) => $.workflow.delivery_modes[mode])}</SelectItem>)}
            </SelectContent>
          </Select>
        </label>
        <Button variant="secondary" size="sm" className="w-full" disabled={openMika.isPending} onClick={() => openMika.mutate()}>
          {t(($) => $.workflow.open_mica)}
        </Button>
        </div>
      )}
      {settings.enabled && executions && executions.total > 0 && (
        <div className="space-y-1 border-t pt-2 text-caption">
          {executions.executions.slice(0, 3).map((execution) => (
            <div key={execution.id} className="flex items-center justify-between gap-2">
              <span className="truncate">{String(execution.snapshot.goal ?? t(($) => $.workflow.execution_fallback))}</span>
              <span className="shrink-0 text-muted-foreground">{execution.state}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
