"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  projectWorkflowSettingsOptions,
  useUpdateProjectWorkflowSettings,
} from "@multica/core/projects/workflow-queries";
import type { ProjectDeliveryMode } from "@multica/core/types";
import { Switch } from "@multica/ui/components/ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";

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
  const update = useUpdateProjectWorkflowSettings(wsId, projectId);
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
        <label className="block space-y-1.5">
          <span className="text-caption text-muted-foreground">{t(($) => $.workflow.delivery_label)}</span>
          <Select value={settings.delivery_mode} onValueChange={(value) => save({ delivery_mode: value as ProjectDeliveryMode })}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              {deliveryModes.map((mode) => <SelectItem key={mode} value={mode}>{t(($) => $.workflow.delivery_modes[mode])}</SelectItem>)}
            </SelectContent>
          </Select>
        </label>
      )}
    </section>
  );
}
