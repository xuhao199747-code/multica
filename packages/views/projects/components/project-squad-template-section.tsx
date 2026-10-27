"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { squadListOptions } from "@multica/core/workspace/queries";
import {
  projectRoleSourceOptions,
  useRestoreProjectSquadConfig,
  useSetProjectCollaborationSquad,
  useSyncProjectSquad,
} from "@multica/core/projects/workflow-queries";
import type { ProjectRoleSource, ProjectRoleSourceDiffEntry, Squad } from "@multica/core/types";
import { AlertDialog, AlertDialogContent } from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";

function agentLabel(entry?: { agent_name?: string; agent_id: string }) {
  if (!entry) return "";
  return entry.agent_name || entry.agent_id.slice(0, 8);
}

export function ProjectSquadTemplateSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const router = useNavigation();
  const paths = useWorkspacePaths();
  const queryClient = useQueryClient();
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: roleSource, error: roleSourceError } = useQuery(projectRoleSourceOptions(wsId, projectId));
  const setSquad = useSetProjectCollaborationSquad(wsId, projectId);
  const syncSquad = useSyncProjectSquad(wsId, projectId);
  const restoreConfig = useRestoreProjectSquadConfig(wsId, projectId);
  const [confirmDialog, setConfirmDialog] = useState<
    | { kind: "preview"; squad: Squad; diff: NonNullable<ProjectRoleSource["diff"]> }
    | { kind: "sync"; diff: NonNullable<ProjectRoleSource["diff"]> }
    | { kind: "restore" }
    | { kind: "unbind" }
    | null
  >(null);

  if (!roleSource) return null;

  const selectedSquadId = roleSource.squad_id ?? "";
  const syncBlocked =
    !roleSource.squad_id ||
    roleSource.squad_archived ||
    roleSource.squad_missing ||
    !!roleSource.mapping_incomplete;
  const blockHint = roleSource.squad_archived
    ? t(($) => $.workflow.squad.squad_archived_hint)
    : roleSource.squad_missing
      ? t(($) => $.workflow.squad.squad_missing_hint)
      : roleSource.mapping_incomplete
        ? t(($) => $.workflow.squad.mapping_incomplete_hint)
        : null;
  const hasDiff =
    roleSource.diff.changes.length > 0 ||
    roleSource.diff.removals.length > 0 ||
    roleSource.diff.adds.length > 0;

  const chooseSquad = async (squadId: string) => {
    if (squadId === selectedSquadId) return;
    if (!squadId) {
      setConfirmDialog({ kind: "unbind" });
      return;
    }
    const squad = squads.find((s) => s.id === squadId);
    if (!squad) return;
    try {
      const res = (await api.setProjectCollaborationSquad(projectId, squadId)) as {
        diff?: ProjectRoleSource["diff"];
      };
      queryClient.invalidateQueries({ queryKey: ["projects", wsId, projectId] });
      setConfirmDialog({ kind: "preview", squad, diff: res.diff ?? roleSource.diff });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  };

  const closeDialog = () => setConfirmDialog(null);

  const confirmDialogBody = () => {
    if (!confirmDialog) return null;
    if (confirmDialog.kind === "preview" || confirmDialog.kind === "sync") {
      const diff = confirmDialog.diff;
      const roleLabel = (role: string) => t(($) => $.workflow.roles[role as "prd" | "developer" | "reviewer"] ?? role);
      const row = (entry: ProjectRoleSourceDiffEntry, variant: "change" | "removal" | "add") => (
        <div
          key={`${variant}-${entry.role}`}
          className={
            variant === "removal"
              ? "rounded-xs bg-destructive/10 px-2 py-1 text-caption"
              : variant === "change"
                ? "rounded-xs bg-amber-500/10 px-2 py-1 text-caption"
                : "rounded-xs bg-accent px-2 py-1 text-caption"
          }
        >
          {variant === "change" &&
            t(($) => $.workflow.squad.diff_change_row, { role: roleLabel(entry.role), from: agentLabel(entry.from), to: agentLabel(entry.to) })}
          {variant === "removal" &&
            t(($) => $.workflow.squad.diff_removal_row, { role: roleLabel(entry.role), agent: agentLabel(entry.removing ?? entry.from) })}
          {variant === "add" &&
            t(($) => $.workflow.squad.diff_add_row, { role: roleLabel(entry.role), agent: agentLabel(entry.to) })}
        </div>
      );
      const isPreview = confirmDialog.kind === "preview";
      return (
        <DiffDialogBody
          title={t(($) => (isPreview ? $.workflow.squad.preview_title : $.workflow.squad.sync_title))}
          description={t(($) => (isPreview ? $.workflow.squad.preview_description : $.workflow.squad.sync_description))}
          cancelLabel={t(($) => $.workflow.squad.cancel)}
          onCancel={closeDialog}
          onConfirm={() => {
            if (isPreview) {
              setSquad.mutate(confirmDialog.squad.id, {
                onSuccess: () => {
                  toast.success(t(($) => $.workflow.squad.confirm_select));
                  closeDialog();
                },
                onError: (err) => toast.error(err instanceof Error ? err.message : String(err)),
              });
            } else {
              syncSquad.mutate(diff.diff_hash, {
                onSuccess: () => {
                  toast.success(t(($) => $.workflow.squad.confirm_sync));
                  closeDialog();
                },
                onError: (err) => toast.error(err instanceof Error ? err.message : String(err)),
              });
            }
          }}
          confirmLabel={t(($) => (isPreview ? $.workflow.squad.confirm_select : $.workflow.squad.confirm_sync))}
          pending={setSquad.isPending || syncSquad.isPending}
        >
          {!isPreview && diff.unchanged.length > 0 && (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.workflow.squad.diff_unchanged, { roles: diff.unchanged.map(roleLabel).join(", ") })}
            </p>
          )}
          {diff.changes.length > 0 && (
            <div className="space-y-1">
              <p className="text-caption font-medium">{t(($) => $.workflow.squad.diff_changes)}</p>
              {diff.changes.map((entry) => row(entry, "change"))}
            </div>
          )}
          {diff.removals.length > 0 && (
            <div className="space-y-1">
              <p className="text-caption font-medium">{t(($) => $.workflow.squad.diff_removals)}</p>
              {diff.removals.map((entry) => row(entry, "removal"))}
            </div>
          )}
          {diff.adds.length > 0 && (
            <div className="space-y-1">
              <p className="text-caption font-medium">{t(($) => $.workflow.squad.diff_adds)}</p>
              {diff.adds.map((entry) => row(entry, "add"))}
            </div>
          )}
        </DiffDialogBody>
      );
    }
    if (confirmDialog.kind === "restore") {
      return (
        <DiffDialogBody
          title={t(($) => $.workflow.squad.restore_title)}
          description={t(($) => $.workflow.squad.restore_description)}
          cancelLabel={t(($) => $.workflow.squad.cancel)}
          onCancel={closeDialog}
          onConfirm={() =>
            restoreConfig.mutate(undefined, {
              onSuccess: () => {
                toast.success(t(($) => $.workflow.squad.confirm_restore));
                closeDialog();
              },
              onError: (err) => toast.error(err instanceof Error ? err.message : String(err)),
            })
          }
          confirmLabel={t(($) => $.workflow.squad.confirm_restore)}
          pending={restoreConfig.isPending}
          destructive
        />
      );
    }
    return (
      <DiffDialogBody
        title={t(($) => $.workflow.squad.unbind_title)}
        description={t(($) => $.workflow.squad.unbind_description)}
        cancelLabel={t(($) => $.workflow.squad.cancel)}
        onCancel={closeDialog}
        onConfirm={() =>
          setSquad.mutate(null, {
            onSuccess: () => {
              toast.success(t(($) => $.workflow.squad.confirm_unbind));
              closeDialog();
            },
            onError: (err) => toast.error(err instanceof Error ? err.message : String(err)),
          })
        }
        confirmLabel={t(($) => $.workflow.squad.confirm_unbind)}
        pending={setSquad.isPending}
      />
    );
  };

  return (
    <div className="space-y-2 border-t pt-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-caption text-muted-foreground">{t(($) => $.workflow.squad.label)}</span>
        {roleSource.squad_id && !roleSource.squad_archived && !roleSource.squad_missing && (
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-2 text-caption"
            onClick={() => roleSource.squad_id && router.push(paths.squadDetail(roleSource.squad_id))}
          >
            {t(($) => $.workflow.squad.edit_squad)}
          </Button>
        )}
      </div>
      <Select
        items={[
          { value: "", label: t(($) => $.workflow.squad.none) },
          ...squads.map((squad) => ({ value: squad.id, label: squad.name })),
        ]}
        value={selectedSquadId}
        onValueChange={(value) => void chooseSquad(value ?? "")}
      >
        <SelectTrigger className="h-8 w-full">
          <SelectValue placeholder={t(($) => $.workflow.squad.none)} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="">{t(($) => $.workflow.squad.none)}</SelectItem>
          {squads.map((squad) => (
            <SelectItem key={squad.id} value={squad.id}>{squad.name}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selectedSquadId === "" && (
        <p className="text-caption text-muted-foreground">{t(($) => $.workflow.squad.none_hint)}</p>
      )}
      {selectedSquadId !== "" && blockHint && (
        <p className="text-caption text-amber-600 dark:text-amber-500">{blockHint}</p>
      )}
      {selectedSquadId !== "" && !blockHint && hasDiff && (
        <p className="text-caption text-muted-foreground">{t(($) => $.workflow.squad.change_hint)}</p>
      )}
      {roleSourceError && (
        <p className="text-caption text-destructive">
          {roleSourceError instanceof Error ? roleSourceError.message : String(roleSourceError)}
        </p>
      )}
      {selectedSquadId !== "" && (
        <div className="flex flex-wrap gap-2">
          <Button
            variant="secondary"
            size="sm"
            disabled={syncBlocked || !hasDiff || syncSquad.isPending}
            onClick={() => setConfirmDialog({ kind: "sync", diff: roleSource.diff })}
          >
            {t(($) => $.workflow.squad.sync)}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={syncBlocked || !roleSource.overrides_present || restoreConfig.isPending}
            onClick={() => setConfirmDialog({ kind: "restore" })}
          >
            {t(($) => $.workflow.squad.restore)}
          </Button>
          {syncBlocked && (
            <span className="self-center text-caption text-muted-foreground">
              {t(($) => $.workflow.squad.sync_unavailable_hint)}
            </span>
          )}
        </div>
      )}
      <AlertDialog open={confirmDialog !== null} onOpenChange={(open) => !open && closeDialog()}>
        <AlertDialogContent className="w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden p-0">
          {confirmDialogBody()}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DiffDialogBody({
  title,
  description,
  children,
  cancelLabel,
  confirmLabel,
  onCancel,
  onConfirm,
  pending,
  destructive,
}: {
  title: string;
  description: string;
  children?: React.ReactNode;
  cancelLabel: string;
  confirmLabel: string;
  onCancel: () => void;
  onConfirm: () => void;
  pending: boolean;
  destructive?: boolean;
}) {
  return (
    <>
      <div className="space-y-2 px-5 pb-4 pt-5">
        <h2 className="text-title-sm font-semibold">{title}</h2>
        <p className="text-body leading-5 text-muted-foreground">{description}</p>
        {children}
      </div>
      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button type="button" variant="outline" className="w-full sm:w-auto" onClick={onCancel} disabled={pending}>
            {cancelLabel}
          </Button>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            className="w-full sm:w-auto"
            onClick={onConfirm}
            disabled={pending}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </>
  );
}
