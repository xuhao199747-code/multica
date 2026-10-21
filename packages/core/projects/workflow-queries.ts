import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { CreateProjectAgentBindingRequest, UpdateProjectWorkflowSettingsRequest } from "../types";
import { projectKeys } from "./queries";

export const projectWorkflowKeys = {
  settings: (wsId: string, projectId: string) =>
    [...projectKeys.detail(wsId, projectId), "workflow"] as const,
  agents: (wsId: string, projectId: string) =>
    [...projectWorkflowKeys.settings(wsId, projectId), "agents"] as const,
};

export function projectExecutionsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: [...projectWorkflowKeys.settings(wsId, projectId), "executions"] as const,
    queryFn: () => api.listProjectExecutions(projectId),
  });
}

export function projectAgentBindingsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectWorkflowKeys.agents(wsId, projectId),
    queryFn: () => api.listProjectAgentBindings(projectId),
  });
}

export function useCreateProjectAgentBinding(wsId: string, projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProjectAgentBindingRequest) => api.createProjectAgentBinding(projectId, data),
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectWorkflowKeys.agents(wsId, projectId) }),
  });
}

export function useDeleteProjectAgentBinding(wsId: string, projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (bindingId: string) => api.deleteProjectAgentBinding(projectId, bindingId),
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectWorkflowKeys.agents(wsId, projectId) }),
  });
}

export function projectWorkflowSettingsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectWorkflowKeys.settings(wsId, projectId),
    queryFn: () => api.getProjectWorkflowSettings(projectId),
  });
}

export function useUpdateProjectWorkflowSettings(wsId: string, projectId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdateProjectWorkflowSettingsRequest) =>
      api.updateProjectWorkflowSettings(projectId, data),
    onSuccess: (settings) => {
      queryClient.setQueryData(projectWorkflowKeys.settings(wsId, projectId), settings);
    },
  });
}
