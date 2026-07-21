import {
  Users, Building2, ArrowRight, Target, Headphones
} from "lucide-react";
/* ----------------------------------------------------------------------------
 * Agency section
 * -------------------------------------------------------------------------- */

export function Agency() {
  const benefits = [
    { icon: Building2, title: "Multi-workspace", desc: "Manage dozens of clients from a single platform. Each workspace has its own accounts, calendar and team members." },
    { icon: Users, title: "Granular permissions", desc: "Assign specific roles: admin, editor, reviewer, viewer. Each client sees only their own content." },
    { icon: Target, title: "Bulk operations", desc: "Publish the same content to different client accounts. Schedule batches of 200+ posts in one click." },
    { icon: Headphones, title: "Priority support", desc: "Dedicated agency support, guaranteed SLAs, assisted onboarding and team training." },
  ];

  return (
    <section id="agency" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-emerald-500 w-[400px] h-[400px] -top-32 -right-32 animate-drift-rev opacity-30" />
        <div className="glow-orb bg-violet-500 w-[360px] h-[360px] -bottom-32 -left-24 animate-drift-slow opacity-25" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">
            <Building2 className="w-3.5 h-3.5 text-emerald-400" />
            <span>For digital agencies</span>
          </div>
          <h2 className="text-display-2 text-white">
            Built for agencies that{" "}
            <span className="text-gradient-animated">publish for dozens of clients.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Manage your clients' entire publishing workflow from a single platform.
            Separate workspaces, granular permissions, bulk operations and unified reports.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {benefits.map((b, i) => (
            <div key={b.title} className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-emerald-400/30 hover:shadow-[0_8px_32px_rgba(16,185,129,0.12)] transition-all duration-300 group ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}>
              <div aria-hidden="true" className="absolute -top-16 -right-16 w-40 h-40 rounded-full bg-emerald-500/10 blur-3xl pointer-events-none group-hover:bg-emerald-500/20 transition-all duration-500" />
              <div className="relative">
                <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-emerald-500/20 to-teal-500/20 flex items-center justify-center text-emerald-300 mb-4 ring-1 ring-emerald-400/20 group-hover:scale-110 transition-transform duration-300">
                  <b.icon className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-2">{b.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed">{b.desc}</p>
              </div>
            </div>
          ))}
        </div>
        <div className="mt-12 surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden text-center animate-fade-up animation-delay-400">
          <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
          <div className="relative">
            <h3 className="text-display-3 text-white mb-3">Ready to scale your agency?</h3>
            <p className="text-sm text-zinc-400 mb-6 max-w-[48ch] mx-auto">
              Unite all your clients on one platform. Reduce publishing time by 80%
              and offer a service your competitors can't match.
            </p>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Start now
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}


