
import { ArrowRight, Shield, Sparkles } from "lucide-react";
import { API_BASE_URL } from "../lib/api";
import { PROVIDERS } from "../lib/providers";

export function Login() {
  return (
    <div className="min-h-screen bg-[#030308] flex flex-col relative isolate">
      {/* Simple gradient background — no ambient orbs */}
      <div className="fixed inset-0 pointer-events-none -z-10"
        style={{
          background: "radial-gradient(600px circle at 20% 10%, rgba(10,132,255,0.15), transparent 60%), radial-gradient(500px circle at 80% 20%, rgba(123,97,255,0.12), transparent 60%)",
        }}
      />

      <div className="flex-1 flex flex-col items-center justify-center px-6 py-16 relative z-10">
        {/* Header */}
        <div className="text-center mb-12 max-w-[640px]">
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full bg-white/[0.06] border border-white/[0.12] text-[12px] text-[#9aa0aa] mb-6 backdrop-blur-sm">
            <Sparkles size={13} className="text-[#7B61FF]" />
            Connect your social accounts
          </div>
          <h1 className="text-[clamp(28px,5vw,48px)] font-extrabold tracking-[-0.02em] mb-4 leading-[1.05]">
            <span className="text-white">Sign in to </span>
            <span className="bg-gradient-to-r from-[#0A84FF] to-[#7B61FF] bg-clip-text text-transparent">
              InstaEdit
            </span>
          </h1>
          <p className="text-[#9aa0aa] text-[17px] leading-relaxed">
            Choose a platform to connect. Secure OAuth — no passwords, no data shared without your consent.
          </p>
        </div>

        {/* Provider cards */}
        <div
          className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 w-full max-w-[820px]"
        >
          {PROVIDERS.map((p, i) => (
            <a
              key={p.id}
              href={`${API_BASE_URL}/api/v1/auth/${p.id}/login`}
              className="group relative bg-white/[0.04] border border-white/[0.10] rounded-2xl p-5 no-underline text-white hover:bg-white/[0.07] hover:border-white/[0.22] hover:-translate-y-[3px] hover:shadow-[0_16px_48px_rgba(0,0,0,0.4)] transition-all duration-300 overflow-hidden"
              style={{ animation: `fadeUp 0.5s cubic-bezier(0.22, 1, 0.36, 1) ${i * 0.08}s both` }}
            >
              <div className={`absolute top-0 left-0 right-0 h-[2px] bg-gradient-to-r ${p.color} opacity-0 group-hover:opacity-100 transition-opacity duration-300 rounded-t-2xl`} />
              <div className={`absolute -inset-2 bg-gradient-to-br ${p.color} opacity-0 group-hover:opacity-[0.06] blur-2xl transition-opacity duration-500 pointer-events-none`} />
              <div className="relative flex items-start gap-4">
                <div
                  className={`w-11 h-11 rounded-xl bg-gradient-to-br ${p.iconBg} flex items-center justify-center text-white shrink-0 shadow-lg group-hover:scale-105 transition-transform duration-300`}
                  style={{ boxShadow: `0 0 20px ${p.glowColor}` }}
                >
                  {p.icon}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center justify-between">
                    <h3 className="font-bold text-[15px] mb-1 text-white group-hover:text-white transition-all duration-300">
                      {p.name}
                    </h3>
                    <ArrowRight size={15} className="text-white/20 group-hover:text-white/70 group-hover:translate-x-[3px] transition-all duration-300 shrink-0" />
                  </div>
                  <p className="text-[12px] text-[#9aa0aa] leading-relaxed group-hover:text-[#b0b8c0] transition-colors duration-300">
                    {p.description}
                  </p>
                </div>
              </div>
            </a>
          ))}
        </div>

        {/* Bottom security note */}
        <div className="mt-12 flex flex-col items-center gap-4">
          <div className="flex items-center gap-2 text-[12px] text-[#6b7280]">
            <Shield size={13} className="text-[#0A84FF]" />
            <span>Official OAuth • No passwords saved • Revoke access at any time</span>
          </div>
        </div>
      </div>
    </div>
  );
}
