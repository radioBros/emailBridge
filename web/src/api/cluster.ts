import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type ClusterView = components["schemas"]["ClusterView"];
export type ClusterNode = components["schemas"]["NodeView"];
export type QueueView = components["schemas"]["QueueView"];
export type ClusterQueueView = components["schemas"]["ClusterQueueView"];
export type QueuePatch = components["schemas"]["PatchQueueInBody"];
export type QueuesOutBody = components["schemas"]["QueuesOutBody"];

export const getCluster = (): Promise<ClusterView> =>
  api.get<ClusterView>("/cluster");

export const drainNode = (nodeId: string): Promise<void> =>
  api.post(`/cluster/nodes/${encodeURIComponent(nodeId)}/drain`);

export const listQueues = async (): Promise<QueueView[]> => {
  const body = await api.get<QueuesOutBody>("/queues");
  return body.data ?? [];
};

export const patchQueue = (
  queue: string,
  body: QueuePatch,
): Promise<ClusterQueueView> =>
  api.patch<ClusterQueueView>(`/queues/${encodeURIComponent(queue)}`, body);
