import { getJob, type Job } from "@/api/deliveries";

const TERMINAL = new Set(["succeeded", "failed", "cancelled"]);

/** Poll a job until it finishes or `signal` aborts. */
export const pollJob = async (
  jobId: string,
  onUpdate: (job: Job) => void,
  signal?: AbortSignal,
  intervalMs = 800,
): Promise<Job> => {
  for (;;) {
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const job = await getJob(jobId);
    onUpdate(job);
    if (TERMINAL.has(job.state)) return job;
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, intervalMs);
      signal?.addEventListener(
        "abort",
        () => {
          clearTimeout(t);
          reject(new DOMException("Aborted", "AbortError"));
        },
        { once: true },
      );
    });
  }
};

export const jobProgressText = (job: Job): string => {
  const p = job.progress;
  if (!p || !p.total) return job.state;
  return `${p.done}/${p.total}`;
};
