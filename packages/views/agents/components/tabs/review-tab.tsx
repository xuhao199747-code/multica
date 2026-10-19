"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberWithUser, ReviewAgentConfig } from "@multica/core/types";
import { projectListOptions } from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Switch } from "@multica/ui/components/ui/switch";
import { TimeInput } from "@multica/ui/components/ui/time-input";
import {
  SettingsCard,
  SettingsRow,
  SettingsSaveState,
  SettingsSection,
  type SettingsSaveStatus,
} from "../../../settings/components/settings-layout";
import { browserTimezone, TimezoneSelect } from "../../../common/timezone-select";
import { useT } from "../../../i18n";

const TIME_RE = /^\d{2}:\d{2}$/;

function emptyDraft(): ReviewAgentConfig {
  return {
    recipient_id: "",
    scope: "workspace",
    project_ids: [],
    instant_enabled: true,
    summary_enabled: false,
    summary_time: "09:30",
    timezone: browserTimezone(),
  };
}

function configOrNull(enabled: boolean, draft: ReviewAgentConfig): ReviewAgentConfig | null {
  if (!enabled) return null;
  return {
    ...draft,
    project_ids: draft.scope === "projects" ? draft.project_ids : [],
  };
}

export function ReviewTab({
  agent,
  members,
  onDirtyChange,
  onSave,
}: {
  agent: Agent;
  members: MemberWithUser[];
  onDirtyChange?: (dirty: boolean) => void;
  onSave: (updates: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const [enabled, setEnabled] = useState(agent.review_config != null);
  const [draft, setDraft] = useState<ReviewAgentConfig>(
    () => agent.review_config ?? emptyDraft(),
  );
  const [status, setStatus] = useState<SettingsSaveStatus>("idle");

  const { data: projects } = useQuery({
    ...projectListOptions(wsId),
    enabled: !!wsId && enabled && draft.scope === "projects",
  });

  const validationError = useMemo(() => {
    if (!enabled) return null;
    if (!draft.recipient_id) return t(($) => $.review.error_recipient);
    if (draft.scope === "projects" && (draft.project_ids ?? []).length === 0)
      return t(($) => $.review.error_projects);
    if (draft.summary_enabled) {
      if (!TIME_RE.test(draft.summary_time)) return t(($) => $.review.error_time);
      if (!draft.timezone) return t(($) => $.review.error_timezone);
    }
    return null;
  }, [enabled, draft, t]);

  const dirty = useMemo(() => {
    const next = configOrNull(enabled, draft);
    // Normalize the saved config through the same shape so field-level
    // differences the server would collapse (e.g. project_ids on a
    // workspace scope) do not read as edits.
    const current = configOrNull(
      agent.review_config != null,
      agent.review_config ?? emptyDraft(),
    );
    return JSON.stringify(next ?? null) !== JSON.stringify(current ?? null);
  }, [enabled, draft, agent.review_config]);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const save = async () => {
    if (validationError || !dirty) return;
    setStatus("saving");
    try {
      await onSave({ review_config: configOrNull(enabled, draft) });
      setStatus("saved");
    } catch {
      setStatus("error");
    }
  };

  const set = <K extends keyof ReviewAgentConfig>(key: K, value: ReviewAgentConfig[K]) =>
    setDraft((prev) => ({ ...prev, [key]: value }));

  return (
    <SettingsSection
      title={t(($) => $.review.section_title)}
      description={t(($) => $.review.section_description)}
    >
      <SettingsCard>
        <SettingsRow
          label={t(($) => $.review.enable_label)}
          description={t(($) => $.review.enable_description)}
        >
          <Switch
            checked={enabled}
            onCheckedChange={(next) => {
              setEnabled(next);
              if (next && !draft.recipient_id && members.length > 0) {
                setDraft((prev) => ({ ...prev, recipient_id: members[0]?.user_id ?? "" }));
              }
            }}
            aria-label={t(($) => $.review.enable_label)}
          />
        </SettingsRow>
      </SettingsCard>

      {enabled ? (
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.review.recipient_label)}
            description={t(($) => $.review.recipient_description)}
          >
            <Select
              items={members.map((m) => ({ value: m.user_id, label: m.name }))}
              value={draft.recipient_id}
              onValueChange={(next) => next && set("recipient_id", next)}
            >
              <SelectTrigger size="sm" className="w-56 rounded-md" aria-label={t(($) => $.review.recipient_label)}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {members.map((m) => (
                  <SelectItem key={m.user_id} value={m.user_id}>
                    {m.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.review.scope_label)}
            description={t(($) => $.review.scope_description)}
          >
            <Select
              items={[
                { value: "workspace", label: t(($) => $.review.scope_workspace) },
                { value: "projects", label: t(($) => $.review.scope_projects) },
              ]}
              value={draft.scope}
              onValueChange={(next) => next && set("scope", next as ReviewAgentConfig["scope"])}
            >
              <SelectTrigger size="sm" className="w-56 rounded-md" aria-label={t(($) => $.review.scope_label)}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="workspace">
                  {t(($) => $.review.scope_workspace)}
                </SelectItem>
                <SelectItem value="projects">{t(($) => $.review.scope_projects)}</SelectItem>
              </SelectContent>
            </Select>
          </SettingsRow>

          {draft.scope === "projects" ? (
            <SettingsRow
              label={t(($) => $.review.projects_label)}
              description={t(($) => $.review.projects_description)}
            >
              <div className="flex max-h-40 w-56 flex-col gap-2 overflow-y-auto rounded-md border border-border p-2">
                {(projects ?? []).length === 0 ? (
                  <p className="text-caption text-muted-foreground">
                    {t(($) => $.review.projects_empty)}
                  </p>
                ) : (
                  (projects ?? []).map((project) => (
                    <label
                      key={project.id}
                      className="flex items-center gap-2 text-caption"
                    >
                      <Checkbox
                        checked={(draft.project_ids ?? []).includes(project.id)}
                        onCheckedChange={(checked) =>
                          set(
                            "project_ids",
                            checked
                              ? [...(draft.project_ids ?? []), project.id]
                              : (draft.project_ids ?? []).filter((id) => id !== project.id),
                          )
                        }
                      />
                      <span className="truncate">{project.title}</span>
                    </label>
                  ))
                )}
              </div>
            </SettingsRow>
          ) : null}

          <SettingsRow
            label={t(($) => $.review.instant_label)}
            description={t(($) => $.review.instant_description)}
          >
            <Switch
              checked={draft.instant_enabled}
              onCheckedChange={(next) => set("instant_enabled", next)}
              aria-label={t(($) => $.review.instant_label)}
            />
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.review.summary_label)}
            description={t(($) => $.review.summary_description)}
          >
            <Switch
              checked={draft.summary_enabled}
              onCheckedChange={(next) => set("summary_enabled", next)}
              aria-label={t(($) => $.review.summary_label)}
            />
          </SettingsRow>

          {draft.summary_enabled ? (
            <>
              <SettingsRow
                label={t(($) => $.review.summary_time_label)}
                description={t(($) => $.review.summary_time_description)}
              >
                <TimeInput
                  value={draft.summary_time}
                  onChange={(next) => set("summary_time", next)}
                  className="w-32"
                />
              </SettingsRow>
              <SettingsRow
                label={t(($) => $.review.timezone_label)}
                description={t(($) => $.review.timezone_description)}
              >
                <TimezoneSelect
                  value={draft.timezone}
                  onValueChange={(next) => set("timezone", next)}
                  browserSuffix={t(($) => $.review.browser_timezone_suffix)}
                  triggerClassName="w-56 rounded-md"
                />
              </SettingsRow>
            </>
          ) : null}
        </SettingsCard>
      ) : null}

      {/* Save controls stay visible while disabled: turning the role off is
          itself a change the user must be able to persist. */}
      <SettingsCard>
        {validationError ? (
          <div className="px-4 py-3">
            <p className="text-caption text-rose-600 dark:text-rose-400" role="alert">
              {validationError}
            </p>
          </div>
        ) : null}

        <div className="flex items-center justify-between gap-3 px-4 py-3">
          <Button size="sm" disabled={!dirty || !!validationError} onClick={save}>
            {t(($) => $.review.save)}
          </Button>
          <SettingsSaveState
            status={status}
            savingLabel={t(($) => $.review.saving)}
            savedLabel={t(($) => $.review.saved)}
            errorLabel={t(($) => $.review.save_failed)}
          />
        </div>
      </SettingsCard>
    </SettingsSection>
  );
}
