import { Link } from "react-router-dom";
import { BarChart3, Calendar, Layout, LogOut } from "lucide-react";

export function Dashboard() {
  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        <div className="py-6">
          <Link to="/" className="text-sm font-medium text-neutral-500 hover:text-black transition-colors no-underline">
            ← Back to home
          </Link>
        </div>

        <div className="flex flex-col items-center justify-center py-12">
          <div className="w-16 h-16 rounded-2xl bg-black flex items-center justify-center mb-6">
            <LogOut size={28} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,36px)] font-extrabold tracking-[-0.02em] mb-3 text-black text-center">
            Dashboard
          </h1>
          <p className="text-neutral-500 text-[17px] mb-10 text-center max-w-[480px]">
            Your connected accounts will appear here after OAuth login.
          </p>

          {/* Placeholder cards */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 w-full max-w-[800px]">
            {[
              { icon: <Calendar size={22} />, title: "Calendar", desc: "Schedule your posts" },
              { icon: <BarChart3 size={22} />, title: "Analytics", desc: "Unified performance" },
              { icon: <Layout size={22} />, title: "Posts", desc: "Create and publish content" },
            ].map((card) => (
              <div
                key={card.title}
                className="bg-white border border-neutral-200 rounded-xl p-6 flex flex-col items-center text-center gap-3 hover:shadow-[0_8px_24px_rgba(0,0,0,0.04)] transition-all"
              >
                 <div className="w-12 h-12 rounded-xl bg-neutral-100 flex items-center justify-center text-black">
                   {card.icon}
                 </div>
                 <div>
                   <h3 className="font-bold text-[15px] text-black mb-1">{card.title}</h3>
                   <p className="text-[13px] text-neutral-500">{card.desc}</p>
                 </div>
               </div>
            ))}
          </div>

          <p className="mt-12 text-[13px] text-neutral-400">
            Connect an account from the{" "}
            <Link to="/login" className="text-[#0A84FF] font-medium hover:underline">
              login page
            </Link>
          </p>
        </div>
      </div>
    </div>
  );
}
