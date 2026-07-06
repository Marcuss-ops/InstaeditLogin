import { Link } from "react-router-dom";

export function CTA() {
  return (
    <section id="pricing" className="py-24 text-center bg-[#050505] text-white">
      <div className="max-w-[1100px] mx-auto px-6">
        <h2 className="text-[clamp(32px,5vw,48px)] font-extrabold tracking-[-0.02em] mb-6 text-white text-glow">
          Ready to create premium videos?
        </h2>
        <Link
          to="/login"
          className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-white text-black no-underline hover:-translate-y-[1px] hover:bg-neutral-100 hover:shadow-[0_0_15px_rgba(255,255,255,0.4)] transition-all"
        >
          Get started today
        </Link>
      </div>
    </section>
  );
}
