"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ExternalLink, RefreshCw } from "lucide-react";
import type { TFunction } from "i18next";
import { api, ApiError } from "@multica/core/api";
import type { SelfUpdateJob, SelfUpdateStatus } from "@multica/core/types";
import { useT } from "../i18n";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@multica/ui/components/ui/alert-dialog";
import {
  SidebarMenuButton,
  SidebarMenuItem,
} from "@multica/ui/components/ui/sidebar";

const UPSTREAM_COMMIT_URL = "https://api.github.com/repos/multica-ai/multica/commits/main";
const CUSTOM_COMMIT_URL = "https://api.github.com/repos/xuhao199747-code/multica/commits/codex/browser-chat-local-sync";
const UPSTREAM_HISTORY_URL = "https://github.com/multica-ai/multica/commits/main";

export type GitHubCommit = {
  sha: string;
  parents: Array<{ sha: string }>;
};

export type UpdateState = "current" | "update-available" | "unknown";

/** A merge commit can contain the upstream revision in either parent. */
export function classifyUpstreamUpdate(
  upstreamSha: string,
  customCommit: GitHubCommit,
): UpdateState {
  if (customCommit.parents.length === 0) return "unknown";
  return customCommit.parents.some((parent) => parent.sha === upstreamSha)
    ? "current"
    : "update-available";
}

async function loadCommit(url: string): Promise<GitHubCommit> {
  const response = await fetch(url, {
    headers: { Accept: "application/vnd.github+json" },
  });
  if (!response.ok) throw new Error(`GitHub returned ${response.status}`);
  return response.json() as Promise<GitHubCommit>;
}

type LayoutTranslator = TFunction<"layout">;

export function isTerminalSelfUpdatePhase(phase: string): boolean {
  return phase === "succeeded" || phase === "failed" || phase === "needs_decision";
}

export function selfUpdateLabel(status: SelfUpdateStatus, t: LayoutTranslator): string {
  if (status.active_job) {
    switch (status.active_job.phase) {
      case "queued": return t(($) => $.sidebar.self_update_phases.queued);
      case "preparing": return t(($) => $.sidebar.self_update_phases.preparing);
      case "awaiting_review": return t(($) => $.sidebar.self_update_phases.awaiting_review);
      case "repairing": return t(($) => $.sidebar.self_update_phases.repairing);
      case "pushing": return t(($) => $.sidebar.self_update_phases.pushing);
      case "backing_up": return t(($) => $.sidebar.self_update_phases.backing_up);
      case "deploying": return t(($) => $.sidebar.self_update_phases.deploying);
      case "verifying": return t(($) => $.sidebar.self_update_phases.verifying);
      case "succeeded": return t(($) => $.sidebar.self_update_phases.succeeded);
      case "failed": return t(($) => $.sidebar.self_update_phases.failed);
      case "needs_decision": return t(($) => $.sidebar.self_update_phases.needs_decision);
      default: return t(($) => $.sidebar.self_update_phase, { phase: status.active_job.phase });
    }
  }
  if (status.capability === "needs_setup") return t(($) => $.sidebar.self_update_needs_setup);
  if (status.capability === "worker_offline") return t(($) => $.sidebar.self_update_worker_offline);
  if (status.check_failed) return t(($) => $.sidebar.update_unknown);
  if (status.capability === "unsupported") return t(($) => $.sidebar.self_update_unsupported);
  if (status.update_available && status.latest_release) {
    return t(($) => $.sidebar.self_update_available, { tag: status.latest_release.tag });
  }
  return t(($) => $.sidebar.update_current);
}

export function UpdateStatus() {
  const { t } = useT("layout");
  const [state, setState] = useState<UpdateState | "checking">("checking");
  const [status, setStatus] = useState<SelfUpdateStatus | null>(null);
  const [job, setJob] = useState<SelfUpdateJob | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [creationFailed, setCreationFailed] = useState(false);
  const [pollingFailed, setPollingFailed] = useState(false);
  const idempotencyKey = useRef("");
  const creatingRef = useRef(false);

  const checkForUpdates = useCallback(async () => {
    setState("checking");
    setPollingFailed(false);
    try {
      const nextStatus = await api.getSelfUpdateStatus();
      setStatus(nextStatus);
      setJob(nextStatus?.active_job ?? null);
      setState(nextStatus ? "current" : "unknown");
    } catch (error) {
      setStatus(null);
      setJob(null);
      if (error instanceof ApiError && error.status === 404) {
        try {
          const [upstream, custom] = await Promise.all([
            loadCommit(UPSTREAM_COMMIT_URL),
            loadCommit(CUSTOM_COMMIT_URL),
          ]);
          setState(classifyUpstreamUpdate(upstream.sha, custom));
        } catch {
          setState("unknown");
        }
      } else {
        setState("unknown");
      }
    }
  }, []);

  useEffect(() => {
    void checkForUpdates();
  }, [checkForUpdates]);

  const jobId = job?.id;
  const jobPhase = job?.phase;
  useEffect(() => {
    if (!jobId || !jobPhase || isTerminalSelfUpdatePhase(jobPhase)) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let cancelled = false;
    let polling = false;
    const poll = async () => {
      if (polling) return;
      polling = true;
      try {
        const nextJob = await api.getSelfUpdateJob(jobId);
        if (cancelled) return;
        if (!nextJob) throw new Error("Invalid update job response");
        setPollingFailed(false);
        if (isTerminalSelfUpdatePhase(nextJob.phase)) {
          if (timer) clearTimeout(timer);
          timer = undefined;
          try {
            const nextStatus = await api.getSelfUpdateStatus();
            if (cancelled) return;
            if (!nextStatus) throw new Error("Invalid update status response");
            setStatus(nextStatus);
          } catch {
            if (!cancelled) setPollingFailed(true);
          }
        }
        if (!cancelled) setJob(nextJob);
        if (!isTerminalSelfUpdatePhase(nextJob.phase) && !cancelled) {
          timer = setTimeout(poll, 5000);
        }
      } catch {
        if (!cancelled) setPollingFailed(true);
        if (!cancelled) timer = setTimeout(() => void poll(), 5000);
      } finally {
        polling = false;
      }
    };
    timer = setTimeout(poll, 5000);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [jobId, jobPhase]);

  const running = !!job && !isTerminalSelfUpdatePhase(job.phase);
  const canStart = state !== "checking" && !!status && status.capability === "ready"
    && status.can_update && !status.check_failed && status.update_available
    && !!status.latest_release && !job;
  const label = state !== "checking" && status
    ? selfUpdateLabel({ ...status, active_job: job }, t)
    : {
    checking: t(($) => $.sidebar.update_checking),
    current: t(($) => $.sidebar.update_current),
    "update-available": t(($) => $.sidebar.update_available),
    unknown: t(($) => $.sidebar.update_unknown),
  }[state];

  async function startUpdate() {
    if (!canStart || !status?.latest_release || creatingRef.current) return;
    creatingRef.current = true;
    setCreating(true);
    setCreationFailed(false);
    try {
      const nextJob = await api.createSelfUpdateJob(status.latest_release.tag, idempotencyKey.current);
      if (!nextJob) throw new Error("Invalid update job response");
      setJob(nextJob);
      setDialogOpen(false);
    } catch {
      setCreationFailed(true);
    } finally {
      creatingRef.current = false;
      setCreating(false);
    }
  }

  const button = (
    <SidebarMenuButton
      className="text-muted-foreground hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
      tooltip={label}
      disabled={state === "checking" || creating || running}
      aria-busy={state === "checking" || creating || running}
      title={canStart ? label : t(($) => $.sidebar.update_check)}
      onClick={canStart ? undefined : () => void checkForUpdates()}
    >
      <RefreshCw className={state === "checking" || running ? "animate-spin" : undefined} />
      <span className="flex-1 truncate" aria-live="polite">{label}</span>
    </SidebarMenuButton>
  );

  return (
    <SidebarMenuItem>
      <AlertDialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (creatingRef.current) return;
          if (open) {
            idempotencyKey.current = `self-update-${crypto.randomUUID()}`;
            setCreationFailed(false);
          }
          setDialogOpen(open);
        }}
      >
        {canStart ? <AlertDialogTrigger render={button} /> : button}
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.sidebar.self_update_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.sidebar.self_update_confirm_description)}</AlertDialogDescription>
          </AlertDialogHeader>
          {creationFailed && <p role="alert" className="text-body text-destructive">{t(($) => $.sidebar.self_update_create_failed)}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={creating} aria-busy={creating}>{t(($) => $.sidebar.self_update_cancel)}</AlertDialogCancel>
            <AlertDialogAction disabled={creating} aria-busy={creating} onClick={() => void startUpdate()}>
              {t(($) => $.sidebar.self_update_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {pollingFailed && <p role="status" className="px-2 text-caption text-muted-foreground">{t(($) => $.sidebar.update_unknown)}</p>}
      {state === "update-available" && !status && (
        <a
          href={UPSTREAM_HISTORY_URL}
          target="_blank"
          rel="noreferrer"
          aria-label={t(($) => $.sidebar.update_view)}
          className="absolute right-1 top-1 rounded-sm p-1 hover:bg-sidebar-accent"
        >
          <ExternalLink className="size-3.5" />
        </a>
      )}
    </SidebarMenuItem>
  );
}
