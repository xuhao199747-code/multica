import type { Workspace } from "../types";
import { useAuthStore } from "../auth";
import { paths } from "./paths";

/**
 * Priority (workspace-first):
 *   workspace[0] → /<first.slug>/issues
 *   no workspace → /workspaces/new
 *
 * A workspace is sufficient to enter its workbench. `onboarded_at` records
 * progress through the optional onboarding flow; it is not an authorization
 * boundary for an existing workspace. This keeps login, direct workspace
 * links, web onboarding URLs, and desktop startup from sending a user who
 * already has a workspace into a route they do not need.
 *
 * Callers that need invitation-aware routing (callback / login) handle
 * the "un-onboarded with pending invites" branch themselves before calling
 * this resolver — this resolver only deals with the post-invite-check
 * destination.
 */
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  _hasOnboarded: boolean,
): string {
  const first = workspaces[0];
  if (first) {
    return paths.workspace(first.slug).issues();
  }
  return paths.newWorkspace();
}

/**
 * Single source of truth: backed by `users.onboarded_at`, which
 * arrives with the user object on every auth response.
 */
export function useHasOnboarded(): boolean {
  return useAuthStore((s) => s.user?.onboarded_at != null);
}
