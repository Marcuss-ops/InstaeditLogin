import { Nav } from "../components/Nav";
import { Sparkles } from "lucide-react";

export function Compose() {
  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="flex-1 flex flex-col items-center justify-center px-6 py-20 text-center">
        <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-blue-500 to-violet-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(123,97,255,0.25)]">
          <Sparkles size={26} className="text-white" />
        </div>
        <h1 className="text-[28px] font-extrabold tracking-[-0.02em] mb-2 text-black">
          Compose
        </h1>
        <p className="text-neutral-500 text-[16px] max-w-[400px]">
          Create and schedule posts for your connected accounts. Coming soon.
        </p>
      </div>
    </div>
  );
}
