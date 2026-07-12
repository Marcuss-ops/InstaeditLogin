import { Nav } from "../components/Nav";
import { Key } from "lucide-react";

export function Settings() {
  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="flex-1 flex flex-col items-center justify-center px-6 py-20 text-center">
        <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-amber-500 to-orange-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(245,158,11,0.25)]">
          <Key size={26} className="text-white" />
        </div>
        <h1 className="text-[28px] font-extrabold tracking-[-0.02em] mb-2 text-black">
          API Settings
        </h1>
        <p className="text-neutral-500 text-[16px] max-w-[400px]">
          Manage your API keys and webhook configuration. Coming soon.
        </p>
      </div>
    </div>
  );
}
