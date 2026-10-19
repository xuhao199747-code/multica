import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import type { ListReviewDecisionsResponse } from "../types";
import { api } from "../api";

export function issueReviewDecisionsOptions(issueId: string) {
  return queryOptions({
    queryKey: ["issue-review-decisions", issueId],
    queryFn: (): Promise<ListReviewDecisionsResponse> => api.listIssueReviewDecisions(issueId),
    enabled: !!issueId,
  });
}

export function useCreateReviewDecision(issueId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      pull_request_id?: string;
      head_sha: string;
      decision: string;
      note?: string;
    }) => api.createIssueReviewDecision(issueId, input),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["issue-review-decisions", issueId] });
    },
  });
}

export function useResolveReviewDecision(issueId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (decisionId: string) => api.resolveIssueReviewDecision(issueId, decisionId),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["issue-review-decisions", issueId] });
    },
  });
}
