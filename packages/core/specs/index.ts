import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type { CreateSpecRequest } from "../types";

export const specKeys = {
  all: (wsId: string) => ["specs", wsId] as const,
  list: (wsId: string, projectId: string) => [...specKeys.all(wsId), "list", projectId] as const,
  detail: (wsId: string, id: string) => [...specKeys.all(wsId), "detail", id] as const,
  versions: (wsId: string, id: string) => [...specKeys.all(wsId), "versions", id] as const,
  comments: (wsId: string, id: string, versionId: string) => [...specKeys.all(wsId), "comments", id, versionId] as const,
  issues: (wsId: string, id: string) => [...specKeys.all(wsId), "issues", id] as const,
};

export const specListOptions = (wsId: string, projectId: string) => queryOptions({ queryKey: specKeys.list(wsId, projectId), queryFn: () => api.listSpecs(projectId) });
export const specDetailOptions = (wsId: string, id: string) => queryOptions({ queryKey: specKeys.detail(wsId, id), queryFn: () => api.getSpec(id) });
export const specVersionsOptions = (wsId: string, id: string) => queryOptions({ queryKey: specKeys.versions(wsId, id), queryFn: () => api.listSpecVersions(id) });
export const specIssuesOptions = (wsId: string, id: string) => queryOptions({ queryKey: specKeys.issues(wsId, id), queryFn: () => api.listSpecIssues(id) });
export const specCommentsOptions = (wsId: string, id: string, versionId: string) => queryOptions({ queryKey: specKeys.comments(wsId, id, versionId), queryFn: () => api.listSpecVersionComments(id, versionId), enabled: !!versionId });

export function useCreateSpec() {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({ mutationFn: (data: CreateSpecRequest) => api.createSpec(data), onSuccess: () => qc.invalidateQueries({ queryKey: specKeys.all(wsId) }) });
}

export function useSpecActions(id: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: specKeys.all(wsId) });
  const publish = useMutation({ mutationFn: (data: { content: string; changelog: string }) => api.publishSpecVersion(id, data), onSuccess: refresh });
  const transition = useMutation({ mutationFn: (to: "draft" | "in_review" | "approved" | "deprecated") => api.transitionSpec(id, to), onSuccess: refresh });
  const comment = useMutation({ mutationFn: ({ versionId, content, parentId }: { versionId: string; content: string; parentId?: string }) => api.createSpecComment(id, versionId, { content, parentId }), onSuccess: refresh });
  const editComment = useMutation({ mutationFn: ({ id: commentId, content, attachmentIds, revision }: { id: string; content: string; attachmentIds: string[]; revision?: number }) => api.updateComment(commentId, content, attachmentIds, undefined, undefined, revision), onSuccess: refresh });
  const resolveComment = useMutation({ mutationFn: ({ id: commentId, resolved }: { id: string; resolved: boolean }) => resolved ? api.unresolveComment(commentId) : api.resolveComment(commentId), onSuccess: refresh });
  return { publish, transition, comment, editComment, resolveComment };
}
