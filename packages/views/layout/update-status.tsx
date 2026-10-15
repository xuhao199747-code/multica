"use client";

import { useCallback, useEffect, useState } from "react";
import { ExternalLink, RefreshCw } from "lucide-react";
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

export function UpdateStatus() {
  const [state, setState] = useState<UpdateState | "checking">("checking");

  const checkForUpdates = useCallback(async () => {
    setState("checking");
    try {
      const [upstream, custom] = await Promise.all([
        loadCommit(UPSTREAM_COMMIT_URL),
        loadCommit(CUSTOM_COMMIT_URL),
      ]);
      setState(classifyUpstreamUpdate(upstream.sha, custom));
    } catch {
      setState("unknown");
    }
  }, []);

  useEffect(() => {
    void checkForUpdates();
  }, [checkForUpdates]);

  const label = {
    checking: "正在检查更新",
    current: "已是最新版本",
    "update-available": "有官方更新",
    unknown: "检查更新失败",
  }[state];

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        className="text-muted-foreground hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
        onClick={() => void checkForUpdates()}
        title="检查 Multica 更新"
      >
        <RefreshCw className={state === "checking" ? "animate-spin" : undefined} />
        <span className="flex-1">{label}</span>
        {state === "update-available" && (
          <a
            href={UPSTREAM_HISTORY_URL}
            target="_blank"
            rel="noreferrer"
            aria-label="查看官方更新"
            className="rounded-sm p-1 hover:bg-sidebar-accent"
            onClick={(event) => event.stopPropagation()}
          >
            <ExternalLink className="size-3.5" />
          </a>
        )}
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}
