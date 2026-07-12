import { Nav } from "../components/Nav";
import { FileText } from "lucide-react";

export function Posts() {
  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="flex-1 flex flex-col items-center justify-center px-6 py-20 text-center">
        <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-emerald-500 to-teal-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(16,185,129,0.25)]">
          <FileText size={26} className="text-white" />
        </div>
        <h1 className="text-[28px] font-extrabold tracking-[-0.02em] mb-2 text-black">
          Posts
        </h1>
        <p className="text-neutral-500 text-[16px] max-w-[400px]">
          View and manage your published and scheduled posts. Coming soon.
        </p>
      </div>
    </div>
  );
}
