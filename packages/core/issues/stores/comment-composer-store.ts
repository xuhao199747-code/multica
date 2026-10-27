import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../../platform/storage";

/**
 * What a reply does by default when it reaches a running agent that would
 * otherwise take it in its current turn: `steer` adds it to that turn,
 * `after_run` starts a new run once the current one ends. Either way the
 * composer's recipient chip can change it for a single message.
 */
export type RunningAgentReply = "steer" | "after_run";

/**
 * Explicit agent target for the member's next comment. `null` preserves the
 * issue or conversation's existing routing; an agent id records an explicit
 * target.
 * Persisted per workspace so a member keeps their choice across reloads.
 */
export type CommentSendAs = string | null;

/**
 * Personal preferences for the issue-detail comment composer.
 *
 * `sticky` pins the bottom comment bar to the scroll viewport so it stays
 * reachable while reading a long timeline. `runningAgentReply` picks the
 * default for a reply to a running agent. Both are personal habits (like
 * theme), so they persist globally via `defaultStorage` rather than
 * per-workspace storage. `sendAs` is workspace-scoped: the keyed map keeps
 * each workspace's last explicit target separate.
 */
interface CommentComposerStore {
  sticky: boolean;
  runningAgentReply: RunningAgentReply;
  /** Workspace id → explicit agent target (absent = existing routing). */
  sendAsByWorkspace: Record<string, CommentSendAs>;
  toggleSticky: () => void;
  setRunningAgentReply: (value: RunningAgentReply) => void;
  setSendAs: (workspaceId: string, value: CommentSendAs) => void;
}

export const useCommentComposerStore = create<CommentComposerStore>()(
  persist(
    (set) => ({
      sticky: true,
      runningAgentReply: "steer",
      sendAsByWorkspace: {},
      toggleSticky: () => set((s) => ({ sticky: !s.sticky })),
      setRunningAgentReply: (runningAgentReply) => set({ runningAgentReply }),
      setSendAs: (workspaceId, value) =>
        set((s) => ({
          sendAsByWorkspace: { ...s.sendAsByWorkspace, [workspaceId]: value },
        })),
    }),
    {
      name: "multica_comment_composer",
      storage: createJSONStorage(() => defaultStorage),
    },
  ),
);
