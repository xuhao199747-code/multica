// Desktop-only helpers for the project_resource local_directory flow.
//
// These wrap the preload `desktopAPI` surface so view components can
// SSR-render on web (where `window.desktopAPI` is undefined) and degrade
// gracefully to no-op promises instead of crashing.

import type { LocalDirectoryExecutionMode } from "@multica/core/types";

export type PickDirectoryResult = {
  ok: boolean;
  path?: string;
  basename?: string;
  reason?: "cancelled" | "no_window" | "error" | "unsupported";
  error?: string;
};

export type ValidateLocalDirectoryResult = {
  ok: boolean;
  reason?:
    | "not_absolute"
    | "not_found"
    | "not_a_directory"
    | "not_readable"
    | "not_writable"
    | "error"
    | "unsupported";
  error?: string;
  /**
   * Whether the directory sits inside a git working tree. Only meaningful when
   * ok=true; absent from an older desktop build, which is why callers must
   * treat `undefined` as "unknown" rather than "not a repo".
   */
  is_git_repo?: boolean;
};

interface DesktopLocalDirectoryAPI {
  pickDirectory?: (defaultPath?: string) => Promise<PickDirectoryResult>;
  validateLocalDirectory?: (
    path: string,
  ) => Promise<ValidateLocalDirectoryResult>;
}

function readDesktopAPI(): DesktopLocalDirectoryAPI | undefined {
  if (typeof window === "undefined") return undefined;
  const api = (window as unknown as { desktopAPI?: DesktopLocalDirectoryAPI })
    .desktopAPI;
  return api;
}

/** True when the renderer is running inside the Electron desktop shell, as
 *  evidenced by the preload-exposed pickDirectory bridge. Avoids hard-coding
 *  navigator/process checks — those vary across electron-vite + jsdom tests. */
export function isDesktopShell(): boolean {
  const api = readDesktopAPI();
  return typeof api?.pickDirectory === "function";
}

export async function pickDirectory(
  defaultPath?: string,
): Promise<PickDirectoryResult> {
  const api = readDesktopAPI();
  if (!api?.pickDirectory) return { ok: false, reason: "unsupported" };
  return api.pickDirectory(defaultPath);
}

export async function validateLocalDirectory(
  path: string,
): Promise<ValidateLocalDirectoryResult> {
  const api = readDesktopAPI();
  if (!api?.validateLocalDirectory) return { ok: false, reason: "unsupported" };
  return api.validateLocalDirectory(path);
}

/**
 * Builds the resource_ref for a local directory attached while creating a
 * project.
 *
 * Lives in the platform module (not beside one dialog) so lightweight entry
 * points — e.g. the chat page's quick-create dialog — don't pull the whole
 * create-project modal into their chunk. Exported so the execution-mode
 * contract can be asserted directly: this is the payload the server stores
 * and the daemon later reads to decide whether tasks edit the user's folder
 * or hand back a branch, and getting `execution_mode` wrong here is invisible
 * until a task runs.
 */
export function buildLocalDirectoryResourceRef({
  localPath,
  daemonId,
  label,
  mode,
}: {
  localPath: string;
  daemonId: string;
  label: string | null;
  mode: LocalDirectoryExecutionMode;
}): Record<string, unknown> {
  return {
    local_path: localPath,
    daemon_id: daemonId,
    ...(label ? { label } : {}),
    execution_mode: mode,
  };
}
