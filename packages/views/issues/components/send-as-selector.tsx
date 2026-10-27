"use client";

import { ChevronDownIcon } from "lucide-react";
import { useActorName } from "@multica/core/workspace/hooks";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useCurrentWorkspace } from "@multica/core/paths";
import { ActorAvatar } from "../../common/actor-avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";
import type { SendAsChoice } from "../hooks/use-send-as-agent";

/**
 * Compact target selector for the comment composers: shows the chosen
 * agent's avatar plus a chevron, and opens a menu listing every agent the
 * member may trigger. Hides entirely when the workspace offers no real
 * choice (only the default agent is visible) — see useSendAsAgent.
 */
export function SendAsSelector({
  choices,
  selectedId,
  disabled,
  onSelect,
}: {
  choices: SendAsChoice[];
  selectedId: string;
  disabled?: boolean;
  onSelect: (agentId: string) => void;
}) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const selected = choices.find((c) => c.agent.id === selectedId);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        disabled={disabled}
        aria-label={t(($) => $.comment.send_as_trigger_aria, {
          name: selected ? getActorName("agent", selected.agent.id) : "",
        })}
        className="flex items-center gap-1 rounded-full p-0.5 text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ActorAvatar
          actorType="agent"
          actorId={selected?.agent.id ?? ""}
          name={selected ? getActorName("agent", selected.agent.id) : undefined}
          avatarUrl={selected?.agent.avatar_url ?? null}
          size="xs"
        />
        <ChevronDownIcon className="size-3" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        {/* Radio items (not plain items) so the menu participates in the
            same selection model as the recipient chips; also the item type
            that activates reliably under jsdom in tests. */}
        <DropdownMenuRadioGroup value={selectedId} onValueChange={onSelect}>
          {choices.map(({ agent, isDefault }) => (
            <SendAsMenuRow
              key={agent.id}
              agentId={agent.id}
              isDefault={isDefault}
            />
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SendAsMenuRow({
  agentId,
  isDefault,
}: {
  agentId: string;
  isDefault: boolean;
}) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const name = getActorName("agent", agentId);
  const ws = useCurrentWorkspace();
  const detail = useAgentPresenceDetail(ws?.id, agentId);
  const availability =
    detail === "loading"
      ? null
      : detail.availability === "online" || detail.availability === "unstable"
        ? t(($) => $.comment.trigger_starts_now)
        : t(($) => $.comment.trigger_starts_when_online);

  return (
    <DropdownMenuRadioItem value={agentId} aria-label={name}>
      <ActorAvatar
        actorType="agent"
        actorId={agentId}
        name={name}
        size="xs"
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate">{name}</span>
        {availability && (
          <span className="block truncate text-caption text-muted-foreground">
            {availability}
          </span>
        )}
      </span>
      {isDefault && (
        <span className="shrink-0 text-caption text-muted-foreground">
          {t(($) => $.comment.send_as_default_badge)}
        </span>
      )}
    </DropdownMenuRadioItem>
  );
}
