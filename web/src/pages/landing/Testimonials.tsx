import { PlayCircle } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Testimonials — student video stories, placed after the founder story and
 * before the FAQ.
 *
 * The grid embeds student YouTube Shorts in 9:16 phone-style frames (same
 * treatment as the FounderStory video).
 * -------------------------------------------------------------------------- */

const VIDEO_TESTIMONIALS = [
  "W15j3auVUcM",
  "FiTk9bAzfh4",
  "-NzSe0k-P-A",
  "Ddb5mFngqcQ",
  "IvBkOutk0-Q",
  "DBuIP0vur8U",
];

export function Testimonials() {
  return (
    <section id="testimonials" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Testimonials</div>
          <h2 className="text-display-2 text-white">
            In their{" "}
            <span className="text-gradient">own words.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Creators, strategists and teams who replaced the manual grind with
            the InstaEdit pipeline — and ship more, every single week.
          </p>
        </div>

        {/* Video testimonials — 6 student Shorts in 9:16 phone frames */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {VIDEO_TESTIMONIALS.map((id, i) => (
            <div
              key={id}
              className={`animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}
            >
              <div className="relative mx-auto w-full max-w-[280px] surface-glass rounded-[2rem] border border-white/15 p-2 shadow-[0_30px_100px_-40px_rgba(124,58,237,0.45)] transition-all duration-300 hover:border-violet-400/40 hover:shadow-[0_30px_100px_-30px_rgba(139,92,246,0.5)]">
                <div className="relative aspect-[9/16] rounded-[1.6rem] overflow-hidden bg-black">
                  <iframe
                    src={`https://www.youtube.com/embed/${id}?playsinline=1`}
                    title={`Student video testimonial ${i + 1}`}
                    className="absolute inset-0 w-full h-full"
                    loading="lazy"
                    referrerPolicy="strict-origin-when-cross-origin"
                    allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                    allowFullScreen
                  />
                </div>
                <div className="flex items-center justify-center gap-1.5 py-2 text-[10px] text-zinc-400">
                  <PlayCircle className="w-3.5 h-3.5 text-violet-300" />
                  <span>Student story</span>
                </div>
              </div>
            </div>
          ))}
        </div>

      </div>
    </section>
  );
}
