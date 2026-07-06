import { Link } from "react-router-dom";
import { ArrowRight, Shield } from "lucide-react";
import { API_BASE_URL } from "../lib/supabase";

const providers = [
  {
    id: "meta",
    name: "Instagram & Facebook",
    description: "Connect Instagram Business and Facebook Pages",
    color: "from-blue-500 to-purple-500",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M13.5 22v-8h2.7l.4-3.2H13.5V8.5c0-.9.3-1.5 1.6-1.5h1.7V4.1c-.3 0-1.3-.1-2.5-.1-2.5 0-4.2 1.5-4.2 4.3v2.5H7.3V14h2.8v8h3.4z" />
      </svg>
    ),
  },
  {
    id: "tiktok",
    name: "TikTok",
    description: "Publish videos directly to your TikTok profile",
    color: "from-gray-800 to-gray-900",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M19.6 8.2c-1.2 0-2.3-.4-3.2-1.1v6.4c0 3.5-2.8 6.3-6.3 6.3S3.8 17 3.8 13.5 6.6 7.2 10.1 7.2c.4 0 .7 0 1 .1v2.8c-.3-.1-.7-.2-1-.2-1.9 0-3.5 1.6-3.5 3.6s1.6 3.5 3.5 3.5 3.5-1.6 3.5-3.5V3.5h2.7c.3 1.2 1.3 2.2 2.5 2.5v2.2z" />
      </svg>
    ),
  },
  {
    id: "twitter",
    name: "X (Twitter)",
    description: "Publish tweets and media to your X profile",
    color: "from-neutral-700 to-neutral-900",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M17.5 4.5h3.1l-6.8 7.8 8 10.6h-6.3l-4.9-6.4-5.6 6.4H2l7.3-8.3L1.7 4.5h6.4l4.4 5.9 5-5.9z" />
      </svg>
    ),
  },
  {
    id: "youtube",
    name: "YouTube",
    description: "Upload videos to your YouTube channel",
    color: "from-red-500 to-red-600",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M21.6 7.2c-.2-.8-.8-1.4-1.6-1.6-1.6-.4-8-.4-8-.4s-6.4 0-8 .4c-.8.2-1.4.8-1.6 1.6C2 8.8 2 12 2 12s0 3.2.4 4.8c.2.8.8 1.4 1.6 1.6 1.6.4 8 .4 8 .4s6.4 0 8-.4c.8-.2 1.4-.8 1.6-1.6.4-1.6.4-4.8.4-4.8s0-3.2-.4-4.8zM10 15.2V8.8l5.2 3.2-5.2 3.2z" />
      </svg>
    ),
  },
];

export function Login() {
  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        {/* Back to home */}
        <div className="py-6">
          <Link to="/" className="text-sm font-medium text-neutral-500 hover:text-black transition-colors no-underline">
            ← Back to home
          </Link>
        </div>

        <div className="flex flex-col items-center justify-center py-12">
          <h1 className="text-[clamp(28px,4vw,40px)] font-extrabold tracking-[-0.02em] mb-3 text-black text-center">
            Connect your accounts
          </h1>
          <p className="text-neutral-500 text-[17px] mb-10 text-center max-w-[480px]">
            Choose a platform to get started. Secure OAuth login — we never save your passwords.
          </p>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 w-full max-w-[640px]">
            {providers.map((p) => (
              <a
                key={p.id}
                href={`${API_BASE_URL}/api/v1/auth/${p.id}/login`}
                className="group relative bg-white border border-neutral-200 rounded-xl p-5 no-underline text-black hover:border-neutral-400 hover:shadow-[0_8px_24px_rgba(0,0,0,0.06)] hover:-translate-y-[2px] transition-all overflow-hidden"
              >
                {/* Gradient bar on hover */}
                <div className={`absolute top-0 left-0 right-0 h-1 bg-gradient-to-r ${p.color} opacity-0 group-hover:opacity-100 transition-opacity rounded-t-xl`} />

                <div className="flex items-start gap-4">
                  <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${p.color} flex items-center justify-center text-white shrink-0`}>
                    {p.icon}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between">
                      <h3 className="font-bold text-[15px] mb-1 text-black">{p.name}</h3>
                      <ArrowRight size={16} className="text-neutral-300 group-hover:text-black group-hover:translate-x-[2px] transition-all" />
                    </div>
                    <p className="text-[13px] text-neutral-500 leading-relaxed">{p.description}</p>
                  </div>
                </div>
              </a>
            ))}
          </div>

          {/* Security note */}
          <div className="mt-10 flex items-center gap-2 text-[13px] text-neutral-400">
            <Shield size={14} className="text-[#0A84FF]" />
            <span>Official OAuth • No passwords saved • Revoke access at any time</span>
          </div>
        </div>
      </div>
    </div>
  );
}
