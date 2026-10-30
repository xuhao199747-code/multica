"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { ArrowLeft, FolderOpen, FolderPlus, MessageSquare } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { useIsCompact } from "@multica/ui/hooks/use-mobile";
import { useRequiredWorkspaceSlug, useWorkspacePaths } from "@multica/core/paths";
import { getCurrentSlug } from "@multica/core/platform";
import { useChatStore } from "@multica/core/chat";
import { useCreateProject, useDeleteProject, useUpdateProject } from "@multica/core/projects";
import { chatQuickActionsPendingOptions } from "@multica/core/chat/queries";
import { useRegenerateChatQuickActions } from "@multica/core/chat/mutations";
import { useQuickActionsPendingTimeout } from "@multica/core/chat/use-quick-actions-pending-timeout";
import { useQuickActionsFailureToast } from "./components/use-quick-actions-failure-toast";
import { useQuery } from "@tanstack/react-query";
import type { Agent, ChatSession, Project } from "@multica/core/types";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import {
  buildLocalDirectoryResourceRef,
  isDesktopShell,
  pickDirectory,
  validateLocalDirectory,
} from "../platform/local-directory";
import { useLocalDaemonStatus } from "../platform/use-local-daemon-status";
import { runtimeListOptions } from "@multica/core/runtimes";
import {
  Select,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { ChatMessageList, ChatMessageSkeleton } from "./components/chat-message-list";
import { ChatInput } from "./components/chat-input";
import { ChatQueue } from "./components/chat-queue";
import { ChatThreadList } from "./components/chat-thread-list";
import { ChatSessionHeader } from "./components/chat-session-header";
import { EmptyState } from "./components/chat-empty-state";
import { useChatController } from "./components/use-chat-controller";
import { OfflineBanner } from "./components/offline-banner";
import { NoAgentBanner } from "./components/no-agent-banner";
import { ArchivedAgentBanner } from "./components/archived-agent-banner";
import { AgentAccessRevokedBanner } from "./components/agent-access-revoked-banner";
import { RuntimeRequiredBanner } from "./components/runtime-required-banner";
import { AgentHandoffDialog } from "./components/agent-handoff-dialog";
import { useVisualViewportKeyboard } from "./components/use-visual-viewport-keyboard";

/**
 * Chat tab — the first-class two-pane surface (thread list on the left,
 * conversation on the right), mirroring the Inbox page layout. Shares all
 * conversation logic with the floating FAB via `useChatController`; the
 * left rail reuses `ChatThreadList`.
 *
 * Selection is URL-addressable via `?session=<id>` so a thread can be
 * deep-linked, opened from a notification, and survive refresh. The chat
 * store's `activeSessionId` stays the source of truth (both surfaces read
 * it); the URL is kept in sync in both directions. `?agent=<id>` is the
 * complementary one-shot deep link for a NEW chat: it starts a fresh compose
 * bound to that agent and is then stripped from the URL.
 *
 * Starting a chat is where the agent is chosen: the header ⊕ opens an agent
 * picker (see NewChatButton), so the compose box no longer needs its own
 * agent selector. Unlike the FAB, this page passes no `contextItems` to
 * `ChatInput`, so its `@` mentions fall back to manual search (issue-comment
 * style).
 */
function isAbsoluteLocalPath(path: string): boolean {
  return path.startsWith("/") || /^[A-Za-z]:[\\/]/.test(path);
}

function localPathBasename(path: string): string | null {
  const segments = path.split(/[\\/]/).filter(Boolean);
  return segments.length > 0 ? (segments[segments.length - 1] ?? null) : null;
}

export function ChatPage() {
  const { t } = useT("chat");
  const { pathname, searchParams, replace } = useNavigation();
  const workspaceSlug = useRequiredWorkspaceSlug();
  const wsPaths = useWorkspacePaths();
  // App Router can retain this page after navigation. Once the URL belongs
  // to another route, this instance must stop reconciling shared chat state.
  // Also check the live workspace mirror in each effect: the incoming layout
  // can rehydrate the store before this page observes the destination URL.
  const isCurrentChatRoute = pathname === wsPaths.chat();
  const isCompact = useIsCompact();
  const keyboard = useVisualViewportKeyboard(isCurrentChatRoute);

  const c = useChatController({
    isActive: isCurrentChatRoute && getCurrentSlug() === workspaceSlug,
  });
  const createProject = useCreateProject();
  const updateProject = useUpdateProject();
  const deleteProject = useDeleteProject();
  const { data: quickActionsPending = null } = useQuery(
    chatQuickActionsPendingOptions(c.activeSessionId ?? ""),
  );
  // Drop a stuck pending marker (dead daemon / failed supplement) so the pill
  // spinner stops and a later refresh starts clean (MUL-5149).
  useQuickActionsPendingTimeout(c.activeSessionId ?? null, quickActionsPending);
  // Toast when an accepted refresh later fails in the daemon (async half).
  useQuickActionsFailureToast(c.activeSessionId ?? null);
  const regenerateQuickActions = useRegenerateChatQuickActions();
  const projectDeleteCopy = {
    title: "删除项目",
    description: "项目中的聊天会保留并归入未归类。本机同步的项目文件夹会移入废纸篓。",
    cancel: "取消",
    confirm: "删除项目",
    failed: "删除项目失败，请稍后重试",
  };
  // The chat page copy is zh-only for now (same as projectDeleteCopy above).
  // Centralizing it here keeps the strings out of JSX literals, which the
  // i18next lint rule requires until this page moves to locale files.
  const projectDialogCopy = {
    title: "新建项目",
    description: "项目用于归纳彼此独立的聊天记录。",
    cancel: "取消",
    submit: "创建项目",
    pickDirectory: "选择本地文件夹",
    pickingDirectory: "正在打开文件选择器…",
    sourceTabPlain: "直接新建",
    sourceTabFolder: "绑定本地文件夹",
    pathLabel: "本地文件夹路径",
    pathRequired: "填写要绑定的文件夹绝对路径",
    pathPlaceholder: "本地文件夹绝对路径，如 /Users/mac/Documents/项目",
    deviceLabel: "选择运行设备",
    deviceOffline: "离线",
    noDaemon: "没有可用的本地设备——在目标设备上运行 multica daemon start 后重试",
    invalidPath: "需要绝对路径（以 / 开头）",
    directoryHint: "智能体将直接在该文件夹中工作。",
    pickFailed: "选择文件夹失败，请重试",
    invalidDirectory: "该文件夹不可用，请重新选择",
    agentPickerTitle: "选择智能体",
    agentPickerDescription: "选择后将在该项目内创建新聊天。",
    agentNeedsRuntime: "需要运行环境",
    agentPickerEmpty: "暂无可用智能体",
  };
  const urlSession = searchParams.get("session") || null;
  const urlAgent = searchParams.get("agent") || null;

  // "Composing a brand-new chat" — the user hit ⊕ but hasn't sent yet, so no
  // session exists. At compact widths this decides list-vs-conversation; on desktop the
  // conversation pane is always mounted so it only needs to reset itself once a
  // real session takes over.
  const [composingNew, setComposingNew] = useState(false);
  const [projectDialogOpen, setProjectDialogOpen] = useState(false);
  const [projectTitle, setProjectTitle] = useState("");
  // Bind the new project to an existing local folder by path. Typing a path
  // works in any browser (the folder lives on the selected device's disk);
  // the desktop shell additionally gets a native folder picker that fills it.
  // Two explicit sources, switched by tabs: "plain" keeps the original
  // title-only flow (the sync process creates the folder), "folder" binds an
  // existing one.
  const desktopShell = isDesktopShell();
  const daemonStatus = useLocalDaemonStatus();
  const [projectSource, setProjectSource] = useState<"plain" | "folder">("plain");
  const [projectLocalPath, setProjectLocalPath] = useState("");
  const [projectLocalLabel, setProjectLocalLabel] = useState<string | null>(null);
  const [projectDaemonId, setProjectDaemonId] = useState<string | null>(null);
  const [projectDirectoryError, setProjectDirectoryError] = useState<string | null>(null);
  const [projectPickingDirectory, setProjectPickingDirectory] = useState(false);
  const resetProjectDialog = () => {
    setProjectTitle("");
    setProjectSource("plain");
    setProjectLocalPath("");
    setProjectLocalLabel(null);
    setProjectDaemonId(null);
    setProjectDirectoryError(null);
  };
  // Devices that can own a local folder: every daemon-registered runtime,
  // deduped by daemon (one device registers one runtime per agent CLI).
  const { data: projectRuntimes = [] } = useQuery(runtimeListOptions(c.wsId));
  const projectDaemonOptions = useMemo(() => {
    const byDaemon = new Map<string, { daemonId: string; label: string; online: boolean }>();
    for (const runtime of projectRuntimes) {
      if (!runtime.daemon_id) continue;
      const label = (runtime.custom_name ?? "").trim() || runtime.name;
      const existing = byDaemon.get(runtime.daemon_id);
      if (!existing) {
        byDaemon.set(runtime.daemon_id, {
          daemonId: runtime.daemon_id,
          label,
          online: runtime.status === "online",
        });
      } else {
        existing.online = existing.online || runtime.status === "online";
      }
    }
    return [...byDaemon.values()].sort((a, b) => Number(b.online) - Number(a.online));
  }, [projectRuntimes]);
  const projectPath = projectLocalPath.trim();
  const projectPathValid = !projectPath || isAbsoluteLocalPath(projectPath);
  // Explicit pick wins, then the desktop shell's own daemon, then the first
  // online device — a sensible default for "my folder on my machine".
  const effectiveProjectDaemonId =
    projectDaemonId ??
    (desktopShell && daemonStatus.daemonId ? daemonStatus.daemonId : null) ??
    projectDaemonOptions.find((option) => option.online)?.daemonId ??
    projectDaemonOptions[0]?.daemonId ??
    null;
  const [deletingProject, setDeletingProject] = useState<Project | null>(null);
  const [selectingAgentForProjectId, setSelectingAgentForProjectId] = useState<string | null>(null);
  useEffect(() => {
    // Read the LIVE store value for the same reason as the session sync
    // effects below: under StrictMode's double-invoke this effect replays
    // with the render-captured snapshot, and a stale non-null session (a
    // persisted chat the URL→store effect already cleared) would revert the
    // composingNew=true that the later `?agent=` intent effect just set.
    if (useChatStore.getState().activeSessionId) setComposingNew(false);
  }, [c.activeSessionId]);

  // Two-way sync between the URL (`?session=`) and the chat store's
  // activeSessionId. Both effects read the LIVE store value via
  // `useChatStore.getState()` rather than the render-captured `c.activeSessionId`.
  // That is what keeps them from fighting on mount: a naive mirror effect fires
  // with the stale (null) snapshot and "corrects" the URL by stripping the
  // session before the URL→store effect has applied — breaking deep links and
  // making selection / new-chat feel unresponsive. Reading getState() sees the
  // value the sibling effect just wrote, so the reconciliation converges in one
  // pass and is idempotent under StrictMode's double-invoke.

  // URL → store: deep link, refresh, notification click, back/forward.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    if (urlSession !== useChatStore.getState().activeSessionId) {
      c.setActiveSession(urlSession);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reconcile URL/route changes, not store updates
  }, [isCurrentChatRoute, workspaceSlug, urlSession]);

  // store → URL: thread selection, "new chat", and sessions created by sending.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    const live = useChatStore.getState().activeSessionId;
    const current = searchParams.get("session") || null;
    if (live !== current) {
      const base = wsPaths.chat();
      replace(live ? `${base}?session=${live}` : base);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reconcile store/route changes, not URL updates
  }, [isCurrentChatRoute, workspaceSlug, c.activeSessionId]);

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_chat_layout",
  });

  // `?agent=` intent bookkeeping. The ref holds the param value already
  // consumed (or superseded) so the effect below fires at most once per deep
  // link — it also bridges the async window between replace() and the
  // searchParams actually dropping the param. Any explicit user action must
  // supersede a still-pending intent: agent/member queries can resolve late,
  // and a deferred intent firing after the user picked a thread (or started
  // another chat) would clobber that choice.
  const consumedAgentIntent = useRef<string | null>(null);
  const supersedeAgentIntent = () => {
    if (urlAgent) consumedAgentIntent.current = urlAgent;
  };

  const handleSelect = (session: ChatSession) => {
    supersedeAgentIntent();
    c.handleSelectSession(session);
    setComposingNew(false);
  };

  // Single archive path for both entry points (thread-list row + conversation
  // header). When the archived chat is the one in view, move the pane off it:
  // on desktop advance to the next chat (Inbox-style); when compact drop back to
  // the list, which reads more naturally than being thrown into an unrelated
  // conversation full-screen. Archiving any other chat leaves the view put.
  const handleArchive = (session: ChatSession) => {
    supersedeAgentIntent();
    if (session.id === c.activeSessionId) {
      if (isCompact) {
        c.setActiveSession(null);
        setComposingNew(false);
      } else {
        c.advanceSelectionAfterArchive(session);
      }
    }
    c.archiveSession(session.id);
  };

  const startNewChat = (agent: Agent | null) => {
    // A manual ⊕ pick outranks a pending deep link; when called FROM the
    // intent effect the ref is already set to this param, so this is a no-op.
    supersedeAgentIntent();
    if (agent) c.handleStartNewChat(agent);
    else c.handleNewChat();
    setComposingNew(true);
  };

  const startProjectChat = (projectId: string) => {
    supersedeAgentIntent();
    // A single runnable agent means there is no real choice to make — skip
    // the picker and start the project chat with it directly. Multiple
    // (or zero) agents still open the picker.
    const runnable = c.availableAgents.filter((a) => !!a.runtime_id);
    if (runnable.length === 1 && runnable[0]) {
      c.handleStartNewChat(runnable[0]);
      c.handleStartProjectChat(projectId);
      setComposingNew(true);
      return;
    }
    setSelectingAgentForProjectId(projectId);
  };

  const startProjectChatWithAgent = (agent: Agent) => {
    if (!selectingAgentForProjectId) return;
    // Reuse the controller's two explicit state transitions: choose the
    // agent first, then scope the fresh draft to this project.
    c.handleStartNewChat(agent);
    c.handleStartProjectChat(selectingAgentForProjectId);
    setSelectingAgentForProjectId(null);
    setComposingNew(true);
  };

  const createNewProject = () => {
    const title = projectTitle.trim();
    if (!title) return;
    const bindFolder = projectSource === "folder";
    if (bindFolder && (!projectPath || !projectPathValid || !effectiveProjectDaemonId)) return;
    // Attach the folder in the same request that creates the project: the
    // server persists both in one transaction, so the sync process sees a
    // finished project on its next poll instead of racing to bind its own
    // sync-root directory.
    const resources =
      bindFolder && projectPath && effectiveProjectDaemonId
        ? [
            {
              resource_type: "local_directory" as const,
              resource_ref: buildLocalDirectoryResourceRef({
                localPath: projectPath,
                daemonId: effectiveProjectDaemonId,
                label: projectLocalLabel ?? localPathBasename(projectPath),
                mode: "in_place",
              }),
            },
          ]
        : undefined;
    createProject.mutate(
      { title, status: "planned", priority: "none", resources },
      {
        onSuccess: () => {
          setProjectDialogOpen(false);
          resetProjectDialog();
          // Keep creation and conversation creation separate: the new project
          // appears in the tree first, then its + action starts a fresh chat.
          // This preserves the explicit Project → Chat hierarchy.
        },
        onError: () => toast.error("创建项目失败，请稍后重试"),
      },
    );
  };

  const handlePickProjectDirectory = async () => {
    if (projectPickingDirectory) return;
    setProjectDirectoryError(null);
    setProjectPickingDirectory(true);
    try {
      const picked = await pickDirectory(projectLocalPath || undefined);
      if (!picked.ok || !picked.path) {
        if (picked.reason && picked.reason !== "cancelled") {
          setProjectDirectoryError(picked.error ?? projectDialogCopy.pickFailed);
        }
        return;
      }
      const validation = await validateLocalDirectory(picked.path);
      if (!validation.ok) {
        setProjectDirectoryError(validation.error ?? projectDialogCopy.invalidDirectory);
        return;
      }
      setProjectLocalPath(picked.path);
      setProjectLocalLabel(picked.basename ?? null);
      // A picked folder is the strongest title hint — prefill it only when
      // the user hasn't typed anything yet.
      if (!projectTitle.trim()) setProjectTitle(picked.basename ?? "");
    } finally {
      setProjectPickingDirectory(false);
    }
  };

  const changeProjectContext = (projectId: string | null) => {
    if (projectId === c.activeProjectId) return;
    c.handleProjectChange(projectId);
    // Removing a project stays in the current conversation. Choosing a
    // project for an existing conversation starts a clean session, and a
    // compact layout must stay in the compose pane after activeSessionId is
    // cleared.
    if (!c.currentSession || projectId !== null) setComposingNew(true);
  };

  // URL → new chat: `?agent=<id>` is the deep link used by "DM" entry points
  // (e.g. the agent detail page) to land on a fresh compose bound to that
  // agent. The permission-filtered agent list loads async, so the intent is
  // consumed on the render where the agent resolves, then the param is
  // stripped so refresh / the session sync above don't replay it. The ref
  // resets once the param is gone so a later identical deep link fires again.
  // A settled miss (access revoked, agent archived, bad id) is a denial: it
  // explains itself with a toast and consumes the intent so a later refetch
  // that surfaces the agent cannot start a chat without a fresh click. While
  // the queries are still loading the intent simply stays pending.
  useEffect(() => {
    if (!isCurrentChatRoute || getCurrentSlug() !== workspaceSlug) return;
    if (!urlAgent) {
      consumedAgentIntent.current = null;
      return;
    }
    if (consumedAgentIntent.current === urlAgent) return;
    const agent = c.availableAgents.find((a) => a.id === urlAgent);
    if (agent) {
      consumedAgentIntent.current = urlAgent;
      startNewChat(agent);
      replace(wsPaths.chat());
      return;
    }
    if (c.agentsSettled) {
      consumedAgentIntent.current = urlAgent;
      toast.error(t(($) => $.page.agent_link_no_access));
      replace(wsPaths.chat());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- consume when the URL param or the resolving agent list changes
  }, [isCurrentChatRoute, workspaceSlug, urlAgent, c.availableAgents, c.agentsSettled]);

  const listHeader = (
    <PageHeader>
      <h1 className="flex-1 text-body font-semibold">{t(($) => $.page.title)}</h1>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={projectDialogCopy.title}
        title={projectDialogCopy.title}
        onClick={() => {
          resetProjectDialog();
          setProjectDialogOpen(true);
        }}
      >
        <FolderPlus className="size-4" />
      </Button>
      <Dialog
        open={projectDialogOpen}
        onOpenChange={(open) => {
          setProjectDialogOpen(open);
          if (!open) resetProjectDialog();
        }}
      >
        <DialogContent>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              createNewProject();
            }}
          >
            <DialogHeader>
              <DialogTitle>{projectDialogCopy.title}</DialogTitle>
              <DialogDescription>{projectDialogCopy.description}</DialogDescription>
            </DialogHeader>
            <Input
              autoFocus
              className="mt-4"
              value={projectTitle}
              onChange={(event) => setProjectTitle(event.target.value)}
              placeholder="项目名称"
              aria-label="项目名称"
            />
            <div className="mt-3 grid grid-cols-2 gap-1 rounded-md bg-muted/60 p-0.5">
              <button
                type="button"
                onClick={() => setProjectSource("plain")}
                className={cn(
                  "rounded-xs px-2 py-1 text-caption transition-colors",
                  projectSource === "plain"
                    ? "bg-background shadow-sm font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {projectDialogCopy.sourceTabPlain}
              </button>
              <button
                type="button"
                onClick={() => setProjectSource("folder")}
                className={cn(
                  "rounded-xs px-2 py-1 text-caption transition-colors",
                  projectSource === "folder"
                    ? "bg-background shadow-sm font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {projectDialogCopy.sourceTabFolder}
              </button>
            </div>
            {projectSource === "folder" && (
              <div className="mt-3 space-y-1.5">
                <div className="flex items-center gap-1.5">
                  <Input
                    value={projectLocalPath}
                    onChange={(event) => {
                      setProjectLocalPath(event.target.value);
                      if (projectDirectoryError) setProjectDirectoryError(null);
                    }}
                    placeholder={projectDialogCopy.pathPlaceholder}
                    aria-label={projectDialogCopy.pathLabel}
                    className="flex-1 font-mono text-caption"
                  />
                  {desktopShell && daemonStatus.running && (
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-sm"
                      onClick={handlePickProjectDirectory}
                      disabled={projectPickingDirectory}
                      aria-label={projectDialogCopy.pickDirectory}
                      title={projectDialogCopy.pickDirectory}
                    >
                      <FolderOpen className="size-3.5" />
                    </Button>
                  )}
                </div>
                {projectPath && projectDaemonOptions.length > 0 && (
                  <Select
                    items={projectDaemonOptions.map((option) => ({
                      value: option.daemonId,
                      label: `${option.label}${!option.online ? `（${projectDialogCopy.deviceOffline}）` : ""}`,
                    }))}
                    value={effectiveProjectDaemonId ?? null}
                    onValueChange={(value) => {
                      if (value) setProjectDaemonId(value);
                    }}
                  >
                    <SelectTrigger
                      className="h-8 w-full text-caption"
                      aria-label={projectDialogCopy.deviceLabel}
                    >
                      <SelectValue placeholder={projectDialogCopy.deviceLabel} />
                    </SelectTrigger>
                  </Select>
                )}
                {!projectPath && (
                  <p className="text-micro text-muted-foreground">{projectDialogCopy.pathRequired}</p>
                )}
                {projectPath && !projectPathValid && (
                  <p className="text-micro text-destructive">{projectDialogCopy.invalidPath}</p>
                )}
                {projectPath && projectPathValid && !effectiveProjectDaemonId && (
                  <p className="text-micro text-amber-600 dark:text-amber-400">
                    {projectDialogCopy.noDaemon}
                  </p>
                )}
                {projectPath && projectPathValid && effectiveProjectDaemonId && (
                  <p className="text-micro text-muted-foreground">{projectDialogCopy.directoryHint}</p>
                )}
                {projectDirectoryError && (
                  <p className="text-micro text-destructive">{projectDirectoryError}</p>
                )}
              </div>
            )}
            <DialogFooter className="mt-4">
              <Button type="button" variant="outline" onClick={() => setProjectDialogOpen(false)}>
                {projectDialogCopy.cancel}
              </Button>
              <Button
                type="submit"
                disabled={
                  !projectTitle.trim() ||
                  createProject.isPending ||
                  (projectSource === "folder" &&
                    (!projectPath || !projectPathValid || !effectiveProjectDaemonId))
                }
              >
                {projectDialogCopy.submit}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={selectingAgentForProjectId !== null}
        onOpenChange={(open) => {
          if (!open) setSelectingAgentForProjectId(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{projectDialogCopy.agentPickerTitle}</DialogTitle>
            <DialogDescription>{projectDialogCopy.agentPickerDescription}</DialogDescription>
          </DialogHeader>
          <div className="mt-3 max-h-80 space-y-1 overflow-y-auto">
            {c.availableAgents.length > 0 ? c.availableAgents.map((agent) => {
              const runnable = !!agent.runtime_id;
              return (
                <Button
                  key={agent.id}
                  type="button"
                  variant="ghost"
                  disabled={!runnable}
                  onClick={() => startProjectChatWithAgent(agent)}
                  className="h-11 w-full justify-start gap-2 px-2"
                >
                  <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent text-caption font-medium">
                    {agent.name.slice(0, 1)}
                  </span>
                  <span className="truncate">{agent.name}</span>
                  {!runnable && <span className="ml-auto text-micro text-muted-foreground">{projectDialogCopy.agentNeedsRuntime}</span>}
                </Button>
              );
            }) : (
              <p className="px-2 py-4 text-center text-caption text-muted-foreground">{projectDialogCopy.agentPickerEmpty}</p>
            )}
          </div>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={deletingProject !== null}
        onOpenChange={(open) => {
          if (!open && !deleteProject.isPending) setDeletingProject(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{projectDeleteCopy.title}</AlertDialogTitle>
            <AlertDialogDescription>
              {projectDeleteCopy.description}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteProject.isPending}>{projectDeleteCopy.cancel}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={!deletingProject || deleteProject.isPending}
              aria-busy={deleteProject.isPending}
              onClick={() => {
                if (!deletingProject) return;
                deleteProject.mutate(deletingProject.id, {
                  onSuccess: () => setDeletingProject(null),
                  onError: () => toast.error(projectDeleteCopy.failed),
                });
              }}
            >
              {projectDeleteCopy.confirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </PageHeader>
  );

  const listBody = (
    <div className="px-2 py-1">
      <ChatThreadList
        sessions={c.sessions}
        agents={c.agents}
        activeSessionId={c.activeSessionId}
        onSelectSession={handleSelect}
        onArchive={handleArchive}
        projects={c.projects}
        onStartProjectChat={startProjectChat}
        onRenameProject={(project, title) => {
          updateProject.mutate(
            { id: project.id, title },
            { onError: () => toast.error("重命名项目失败，请稍后重试") },
          );
        }}
        onDeleteProject={setDeletingProject}
      />
    </div>
  );

  // The conversation pane: message list / skeleton / empty above a persistent
  // banner + input. Identical composition to the floating window's body, so a
  // brand-new chat (no active session) shows the agent-aware empty state + input.
  // No compose-box agent selector — the agent is fixed when the chat starts.
  // `@container`: the conversation column's gutter (CHAT_GUTTER) widens with
  // THIS pane, which the user resizes independently of the browser window.
  const queuedTasks = c.pendingTask?.queued_tasks ?? [];
  const conversation = (
    <div className="flex flex-1 flex-col min-h-0 @container">
      {c.currentSession && (
        <ChatSessionHeader
          session={c.currentSession}
          agent={c.activeAgent}
          onArchive={handleArchive}
        />
      )}
      {c.showSkeleton ? (
        <ChatMessageSkeleton />
      ) : c.hasMessages ? (
        <ChatMessageList
          key={c.activeSessionId}
          messages={c.messages}
          pendingTask={c.pendingTask}
          availability={c.availability}
          firstItemIndex={c.firstItemIndex}
          hasOlderMessages={c.hasOlderMessages}
          isFetchingOlderMessages={c.isFetchingOlderMessages}
          onLoadOlderMessages={() => void c.fetchOlderMessages()}
          onQuickAction={(action) => c.handleSend(action.prompt)}
          quickActionsDisabled={
            !!c.pendingTaskId ||
            c.isSessionArchived ||
            c.isAgentArchived ||
            c.isAgentAccessRevoked ||
            !c.isAgentRuntimeBound ||
            c.noAgent
          }
          onRegenerateQuickActions={(message) =>
            c.activeSessionId
              ? regenerateQuickActions.mutateAsync({
                  sessionId: c.activeSessionId,
                  messageId: message.id,
                })
              : undefined
          }
          quickActionsPendingMessageId={quickActionsPending?.message_id ?? null}
        />
      ) : (
        <EmptyState
          agent={c.activeAgent}
          hasSessions={c.sessions.length > 0}
          onPickPrompt={c.prefillConversationStarter}
          customizeHref={c.customizeConversationStartersHref}
        />
      )}

      {c.isAgentAccessRevoked ? (
        <AgentAccessRevokedBanner agentName={c.activeAgent?.name} />
      ) : c.noAgent ? (
        <NoAgentBanner />
      ) : c.isAgentArchived ? (
        <ArchivedAgentBanner agentName={c.activeAgent?.name} />
      ) : !c.isAgentRuntimeBound && c.activeAgent ? (
        <RuntimeRequiredBanner
          agentId={c.activeAgent.id}
          agentName={c.activeAgent.name}
        />
      ) : (
        <OfflineBanner agentName={c.activeAgent?.name} availability={c.availability} />
      )}

      <ChatQueue
        tasks={queuedTasks}
        headStatus={c.pendingTask?.status}
        onSendNow={c.handleSendQueuedTaskNow}
        sendNowDisabled={c.isAgentAccessRevoked}
        onEdit={c.handleEditQueuedTask}
        onRemove={c.handleRemoveQueuedTask}
        onClear={c.handleClearQueuedTasks}
      />

      <ChatInput
        onSend={c.handleSend}
        restoreDraftRequest={c.restoreDraftRequest}
        conversationStarterRequest={c.conversationStarterRequest}
        onConversationStarterApplied={c.handleConversationStarterApplied}
        onRestoreDraftApplied={c.handleRestoreDraftApplied}
        uploadEnabled={c.uploadEnabled && !c.isAgentAccessRevoked}
        onStop={c.handleStop}
        isRunning={!!c.pendingTaskId}
        allowSubmitWhileRunning={c.pendingTask?.supports_queue === true}
        disabled={
          c.isSessionArchived ||
          c.isAgentArchived ||
          c.isAgentAccessRevoked ||
          !c.isAgentRuntimeBound
        }
        noAgent={c.noAgent}
        agentArchived={c.isAgentArchived}
        agentAccessRevoked={c.isAgentAccessRevoked}
        agentRuntimeRequired={!c.isAgentRuntimeBound}
        agentName={c.activeAgent?.name}
        projects={c.projects}
        projectId={c.activeProjectId}
        projectContextUnsupported={c.projectContextUnsupported}
        onProjectChange={changeProjectContext}
        isProjectUpdating={c.isProjectUpdating}
        focusRequest={c.focusInputRequest}
      />
    </div>
  );

  const handoffDialog = c.handoffDialog && (
    <AgentHandoffDialog
      dialog={c.handoffDialog}
      onDraftChange={c.handoffActions.setDraft}
      onDestinationChange={c.handoffActions.setDestination}
      onCancel={c.handoffActions.closeDialog}
      onSend={c.handoffActions.deliver}
    />
  );

  let pageContent: ReactNode;

  // -- Compact: list / conversation toggle -----------------------------------
  if (isCompact) {
    if (c.activeSessionId || composingNew) {
      pageContent = (
        <div
          className="flex flex-1 flex-col min-h-0"
          style={keyboard ? { flex: "none", height: keyboard.viewportHeight } : undefined}
        >
          <div className="flex h-12 shrink-0 items-center border-b px-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                c.setActiveSession(null);
                setComposingNew(false);
              }}
              className="gap-1.5 text-muted-foreground"
            >
              <ArrowLeft className="h-4 w-4" />
              {t(($) => $.page.title)}
            </Button>
          </div>
          {conversation}
        </div>
      );
    } else {
      pageContent = (
        <div className="flex flex-1 flex-col min-h-0">
          {listHeader}
          <div className="flex-1 min-h-0 overflow-y-auto">{listBody}</div>
        </div>
      );
    }
  } else {
    // -- Desktop: resizable two-panel. The conversation pane appears only once
    // there is a chat target — an open thread or a new chat whose agent was just
    // picked via ⊕. With nothing selected there is no agent, so we show a neutral
    // prompt instead of an orphaned compose box. -------------------------------
    const hasTarget = !!c.activeSessionId || composingNew;
    pageContent = (
      <ResizablePanelGroup
        orientation="horizontal"
        className="flex-1 min-h-0"
        defaultLayout={defaultLayout}
        onLayoutChanged={onLayoutChanged}
      >
        <ResizablePanel
          id="list"
          defaultSize={260}
          minSize={240}
          maxSize={480}
          groupResizeBehavior="preserve-pixel-size"
        >
          <div className="flex flex-col border-r h-full">
            {listHeader}
            <div className="flex-1 min-h-0 overflow-y-auto">{listBody}</div>
          </div>
        </ResizablePanel>
        <ResizableHandle />
        <ResizablePanel id="detail" minSize="40%">
          <div className="flex flex-col min-h-0 h-full">
            {hasTarget ? (
              conversation
            ) : (
              <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                <MessageSquare className="h-10 w-10 text-faint-foreground" />
                <p className="text-body">{t(($) => $.page.select_prompt)}</p>
              </div>
            )}
          </div>
        </ResizablePanel>
      </ResizablePanelGroup>
    );
  }

  return <>{pageContent}{handoffDialog}</>;
}
