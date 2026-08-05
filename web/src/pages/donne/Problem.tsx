import { X } from "lucide-react";
import { SectionVideo } from "./SectionVideo";
import { PROBLEM } from "./content";

/**
 * Sezione "Il Problema" — i 4 punti di dolore quotidiani. Sfondo con
 * video in loop e overlay crema per la leggibilità del tema chiaro.
 */
export function Problem() {
  return (
    <section id="problema" className="relative py-24 sm:py-32 overflow-hidden bg-[#F6F3F7]">
      <SectionVideo src={PROBLEM.bgVideo} />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="absolute w-[420px] h-[420px] -top-24 -right-24 rounded-full bg-[#F3D9DC]/40 blur-[110px]" />
        <div className="absolute w-[380px] h-[380px] -bottom-24 -left-24 rounded-full bg-[#F0E2F4]/40 blur-[110px]" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="donne-video-scrim max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{PROBLEM.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">
            {PROBLEM.titleStart}
            <span className="text-gradient-coral">{PROBLEM.titleAccent}</span>
          </h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch]">
            {PROBLEM.subtitleStart}
            <span className="font-semibold text-[#C0655C]">{PROBLEM.subtitleAccent}</span>
            {PROBLEM.subtitleEnd}
            <span className="font-semibold text-[#C0655C]">{PROBLEM.subtitleAccent2}</span>
            {PROBLEM.subtitleEnd2}
          </p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {PROBLEM.items.map((item, i) => (
            <div
              key={item.title}
              className={`donne-card p-6 animate-fade-up hover:border-[#E2C9CD] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-7 h-7 shrink-0 items-center justify-center rounded-lg bg-[#F9ECEC] ring-1 ring-[#EED6D8] text-[#C0655C]">
                  <X className="w-4 h-4" />
                </span>
                <div>
                  <h3 className="text-display-3 text-[#4A3E56] mb-1.5">{item.title}</h3>
                  <p className="text-sm text-[#6E6677] leading-relaxed">{item.description}</p>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
