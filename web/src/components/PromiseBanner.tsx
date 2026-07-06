import { cn } from "../lib/utils";

const pipelineSteps = [
  { label: "Idea", active: false },
  { label: "Transcribe", active: false },
  { label: "Edit", active: false },
  { label: "Caption", active: false },
  { label: "Publish", active: true },
];

export function PromiseBanner() {
  return (
    <section className="promise-banner" id="promise">
      <div className="promise-container reveal">
        <div className="promise-pipeline" aria-label="Editorial pipeline">
          {pipelineSteps.map((step, i) => (
            <div className="promise-pipeline-row" key={step.label}>
              <div className={cn("promise-pipeline-step", { "promise-pipeline-step-active": step.active })}>
                <span className="promise-pipeline-num">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span className="promise-pipeline-label">{step.label}</span>
              </div>
              {i < pipelineSteps.length - 1 && (
                <span className="promise-pipeline-arrow" aria-hidden="true">
                  ▸
                </span>
              )}
            </div>
          ))}
        </div>

        <h2 className="promise-headline">
          Completely relax while we create your videos—from ideas to video.
        </h2>

        <p className="promise-sub">
          From transcribing your raw footage to the final, captioned, multi-format
          edit, our top-tier editors handle every step end-to-end.
        </p>

        <p className="promise-tag">
          <span className="promise-tag-dot" aria-hidden="true"></span>
          You bring the idea. We bring the finished video.
        </p>
      </div>
    </section>
  );
}
