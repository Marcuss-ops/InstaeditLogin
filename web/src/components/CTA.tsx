import { Link } from "react-router-dom";

export function CTA() {
  return (
    <section id="pricing" className="py-24 text-center">
      <div className="max-w-[1100px] mx-auto px-6">
        <h2 className="text-[clamp(32px,5vw,48px)] font-extrabold tracking-[-0.02em] mb-6 text-black">
          Ready to post everywhere?
        </h2>
        <Link
          to="/login"
          className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-black text-white no-underline hover:-translate-y-[1px] hover:bg-neutral-900 transition-all"
        >
          Create free account
        </Link>
      </div>
    </section>
  );
}
