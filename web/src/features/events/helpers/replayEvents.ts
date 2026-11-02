import { replayEventsToEndpoint, type EventsReplayBody } from "@/api/events";
import { getJob, type Job } from "@/api/deliveries";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

export const jobProgressText = (job: Job): string => {
  const p = job.progress;
  if (!p || !p.total) return job.state;
  return `${p.done}/${p.total}`;
};

export const pollReplayJob = async (
  jobId: string,
  onUpdate: (job: Job) => void,
  intervalMs = 800,
): Promise<Job> => {
  for (;;) {
    const job = await getJob(jobId);
    onUpdate(job);
    if (TERMINAL.has(job.state)) return job;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
};

export const startEventsReplay = async (
  body: EventsReplayBody,
  onUpdate: (job: Job) => void,
): Promise<Job> => {
  const res = await replayEventsToEndpoint(body);
  return pollReplayJob(res.job.id, onUpdate, 50);
};
