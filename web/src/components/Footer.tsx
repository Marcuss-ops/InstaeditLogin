export function Footer() {
  return (
    <footer className="border-t border-neutral-900 py-8 bg-[#050505]">
      <div className="max-w-[1100px] mx-auto px-6 flex justify-between items-center gap-4 flex-wrap text-[13px] text-neutral-500">
        <span>© 2026 InstaEdit</span>
        <div className="flex gap-5">
          <a href="#" className="text-neutral-500 no-underline hover:text-white transition-colors">
            Privacy
          </a>
          <a href="#" className="text-neutral-500 no-underline hover:text-white transition-colors">
            Terms
          </a>
          <a href="#" className="text-neutral-500 no-underline hover:text-white transition-colors">
            Contact
          </a>
        </div>
      </div>
    </footer>
  );
}
