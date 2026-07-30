;
/* ----------------------------------------------------------------------------
 * Who Are We
 * -------------------------------------------------------------------------- */

export function WhoAreWe() {
  return (
    <section id="who-are-we" className="relative overflow-hidden">
      <div className="relative h-[70vh] min-h-[500px] flex items-center justify-center overflow-hidden">
        <img src="/founder.jpg" alt="InstaEdit team working on video automation" className="absolute inset-0 w-full h-full object-cover" />
        <div className="absolute inset-0 bg-black/65" />
        <div className="absolute inset-0 bg-gradient-to-t from-[#030308] via-transparent to-transparent" />
        <div className="relative z-10 text-center px-6 max-w-4xl mx-auto animate-fade-up">
          <h2 className="text-display-1 text-white mb-6">
            We make video accessible{" "}
            <span className="text-gradient-animated">for everyone.</span>
          </h2>
          <p className="text-body-lg text-zinc-300/90 max-w-[55ch] mx-auto">
            Our mission is to remove every barrier between a creator and their audience —
            so anyone, anywhere, can publish professional content without a studio, team or budget.
          </p>
        </div>
      </div>
      <div className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
        <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
        <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-2 gap-16 items-start">
          <div className="animate-fade-up">
            <div className="text-eyebrow text-violet-300/90 mb-3">Our mission</div>
            <h2 className="text-display-2 text-white mb-6">
              Automate video creation{" "}
              <span className="text-gradient-animated">for everyone.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-6">
              We exist to help anyone work for themselves. Creating content shouldn't
              require a whole production team — and publishing on every platform shouldn't
              take all day. With ChronoN, our proprietary AI engine, even students with
              no budget can create professional videos automatically.
            </p>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-8">
              InstaEdit automates the entire pipeline: upload once, and we handle encoding,
              subtitles, thumbnails, scheduling and publishing on YouTube, TikTok, Instagram and more —
              so you can focus on what you love: creating.
            </p>
            <div className="grid grid-cols-3 gap-4">
              {[{ v: "7", l: "Platforms" }, { v: "50+", l: "Languages" }, { v: "24/7", l: "Uptime" }].map((s) => (
                <div key={s.l} className="surface-card p-4 text-center">
                  <div className="text-xl font-bold text-white tabular-nums">{s.v}</div>
                  <div className="text-eyebrow text-zinc-500 mt-1">{s.l}</div>
                </div>
              ))}
            </div>
          </div>
          <div className="relative animate-fade-up animation-delay-200">
            <div className="surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden shadow-[0_30px_100px_-40px_rgba(124,58,237,0.4)]">
              <div aria-hidden="true" className="absolute -top-20 -right-20 w-60 h-60 rounded-full bg-violet-500/25 blur-3xl pointer-events-none" />
              <div className="relative">
                <div className="text-eyebrow text-zinc-200 mb-4">A message from the founder</div>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  Growing up as the child of immigrants, I always looked for ways to build something on my own.
                  When I started creating videos, I fell in love with the freedom of being my own boss
                  — managing my own hours, following my ideas.
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  But I also immediately realized how hard it was to publish everywhere. The 14-tab workflow,
                  re-encoding, manual subtitles — it felt like a full-time job just to hit "publish."
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  That's why I created InstaEdit. It's the all-in-one video publishing tool I wish I had
                  had from day one. We automate all the "business stuff" so you can spend
                  your time doing what you love — creating great content.
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-6">
                  We're on a mission to let anyone earn a living working for themselves,
                  and we're grateful you're here. Creating content is really hard. InstaEdit is here to
                  help you breathe a little easier.
                </p>
                <blockquote className="border-l-2 border-violet-400/50 pl-4">
                  <p className="text-sm text-zinc-200 italic leading-relaxed">Best regards,</p>
                  <p className="text-sm text-white font-semibold mt-2">The InstaEdit Team</p>
                </blockquote>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}


