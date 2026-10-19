"use client";

import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, CircleDashed, CircleSlash, Clock } from "lucide-react";
import {
  issueReviewDecisionsOptions,
  useCreateReviewDecision,
  useResolveReviewDecision,
} from "@multica/core/issues";
import type { GitHubPullRequest, ReviewDecision } from "@multica/core/types";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";

function shortSha(sha: string): string {
  return sha.length > 7 ? sha.slice(0, 7) : sha;
}

function currentDecisionFor(
  decisions: ReviewDecision[],
  pr: GitHubPullRequest,
): ReviewDecision | null {
  const head = pr.head_sha ?? "";
  return (
    decisions.find(
      (d) =>
        d.pull_request_id === pr.id &&
        d.head_sha === head &&
        d.superseded_at == null &&
        d.resolved_at == null,
    ) ?? null
  );
}

function DecisionPill({ decision }: { decision: ReviewDecision }) {
  const { t } = useT("issues");
  const config = (() => {
    switch (decision.decision) {
      case "approved":
        return {
          icon: CheckCircle2,
          className:
            "bg-emerald-600/10 text-emerald-700 dark:bg-emerald-400/15 dark:text-emerald-400",
          label: t(($) => $.review_decisions.current_approved),
        };
      case "changes_requested":
        return {
          icon: CircleSlash,
          className: "bg-rose-600/10 text-rose-700 dark:bg-rose-400/15 dark:text-rose-400",
          label: t(($) => $.review_decisions.current_changes),
        };
      case "deferred":
        return {
          icon: Clock,
          className:
            "bg-amber-600/10 text-amber-700 dark:bg-amber-400/15 dark:text-amber-400",
          label: t(($) => $.review_decisions.current_deferred),
        };
      default:
        return null;
    }
  })();
  if (!config) return null;
  const Icon = config.icon;
  return (
    <span
      data-testid="review-decision-pill"
      className={cn(
        "inline-flex h-4.5 shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-1.5 text-micro font-medium",
        config.className,
      )}
    >
      <Icon className="size-3 shrink-0" />
      {config.label}
      {decision.stale ? (
        <span className="text-muted-foreground">
          · {t(($) => $.review_decisions.stale_badge)}
        </span>
      ) : null}
    </span>
  );
}

function DecisionRow({ issueId, pr }: { issueId: string; pr: GitHubPullRequest }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const { data } = useQuery(issueReviewDecisionsOptions(issueId));
  const create = useCreateReviewDecision(issueId);
  const resolve = useResolveReviewDecision(issueId);
  const decision = currentDecisionFor(data?.decisions ?? [], pr);

  if (pr.head_sha == null || pr.head_sha === "") return null;

  const decide = (next: "approved" | "changes_requested" | "deferred") =>
    create.mutate(
      { pull_request_id: pr.id, head_sha: pr.head_sha ?? "", decision: next },
      { onError: () => toast.error(t(($) => $.review_decisions.error)) },
    );

  return (
    <div className="flex items-center gap-2 px-2 py-1 text-micro text-muted-foreground">
      <span className="font-mono">#{pr.number}</span>
      <span className="font-mono">{shortSha(pr.head_sha)}</span>
      {decision ? (
        <>
          <DecisionPill decision={decision} />
          <span className="truncate">{timeAgo(decision.created_at)}</span>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  className="ml-auto rounded-md px-1.5 py-0.5 text-micro font-medium text-foreground underline decoration-border underline-offset-2 hover:decoration-foreground"
                >
                  {t(($) => $.review_decisions.menu_label)}
                </button>
              }
            />
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuItem onClick={() => decide("approved")}>
                {t(($) => $.review_decisions.decide_approved)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => decide("changes_requested")}>
                {t(($) => $.review_decisions.decide_changes)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => decide("deferred")}>
                {t(($) => $.review_decisions.decide_deferred)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => resolve.mutate(decision.id)}>
                {t(($) => $.review_decisions.resolve)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </>
      ) : (
        <>
          <CircleDashed className="size-3 shrink-0" />
          <span>{t(($) => $.review_decisions.current_none)}</span>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  className="ml-auto rounded-md px-1.5 py-0.5 text-micro font-medium text-foreground underline decoration-border underline-offset-2 hover:decoration-foreground"
                >
                  {t(($) => $.review_decisions.menu_label)}
                </button>
              }
            />
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuItem onClick={() => decide("approved")}>
                {t(($) => $.review_decisions.decide_approved)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => decide("changes_requested")}>
                {t(($) => $.review_decisions.decide_changes)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => decide("deferred")}>
                {t(($) => $.review_decisions.decide_deferred)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </>
      )}
    </div>
  );
}

/**
 * Per-PR review decisions (DAWE-20). Records the recipient's call against the
 * PR's exact current head; a newer decision supersedes the old one, and an
 * approved decision explicitly leaves the deploy call to the user — nothing
 * here merges or deploys.
 */
export function ReviewDecisions({
  issueId,
  prs,
}: {
  issueId: string;
  prs: GitHubPullRequest[];
}) {
  const { t } = useT("issues");
  const { data } = useQuery(issueReviewDecisionsOptions(issueId));
  const decidable = prs.filter((pr) => pr.head_sha != null && pr.head_sha !== "");
  const approvedCurrent = decidable.some(
    (pr) => currentDecisionFor(data?.decisions ?? [], pr)?.decision === "approved",
  );
  if (decidable.length === 0) return null;

  return (
    <div className="mt-1 border-t border-border pt-2">
      <p className="px-2 pb-1 text-micro font-medium text-muted-foreground">
        {t(($) => $.review_decisions.section_title)}
      </p>
      <div className="space-y-0.5">
        {decidable.map((pr) => (
          <DecisionRow key={pr.id} issueId={issueId} pr={pr} />
        ))}
      </div>
      {approvedCurrent ? (
        <p className="px-2 pt-1.5 text-micro text-muted-foreground">
          {t(($) => $.review_decisions.approved_deploy_hint)}
        </p>
      ) : null}
    </div>
  );
}
