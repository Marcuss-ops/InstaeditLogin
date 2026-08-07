import { useEffect } from "react";
import { redirectToInstaEditor } from "../../features/youtube/api/editorSessionsApi";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, ExternalLink, ImageIcon, Loader2 } from "lucide-react";

const INSTAEDITOR_BASE_PATH = "/dark_editor_v2";

function editorURL(projectId: string | undefined): string {
  if (!projectId) return `${INSTAEDITOR_BASE_PATH}/`;
  return `${INSTAEDITOR_BASE_PATH}/editor/${encodeURIComponent(projectId)}`;
}

export function CoversPage() {
  const { projectId } = useParams<{ projectId?: string }>();
  const target = editorURL(projectId);

  useEffect(() => {
    // This page is only a route handoff. The editor is a separate SPA;
    // there is deliberately no iframe or shared frontend surface.
    redirectToInstaEditor(target);
  }, [target]);

  return (
    <main className="flex min-h-full items-center justify-center bg-[#030308] p-8 text-[#e8e8ef]">
      <section className="w-full max-w-md rounded-2xl border border-white/[0.10] bg-white/[0.04] p-6 text-center shadow-2xl">
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-violet-500/15 text-violet-200">
          <ImageIcon size={22} aria-hidden="true" />
        </div>
        <h1 className="text-lg font-bold text-white">Apertura InstaEditor</h1>
        <p className="mt-2 text-sm leading-6 text-[#9aa0aa]">
          Stai per lasciare InstaEdit e aprire InstaEditor in una pagina separata.
        </p>
        <Loader2 className="mx-auto mt-5 h-5 w-5 animate-spin text-violet-300" aria-label="Reindirizzamento" />
        <div className="mt-6 flex items-center justify-center gap-2">
          <Link
            to="/app/dashboard"
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] px-3 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white no-underline"
          >
            <ArrowLeft size={14} aria-hidden="true" /> Dashboard
          </Link>
          <a
            href={target}
            className="inline-flex items-center gap-1.5 rounded-lg bg-violet-500 px-3 py-2 text-xs font-bold text-white hover:bg-violet-400 no-underline"
          >
            Apri editor <ExternalLink size={14} aria-hidden="true" />
          </a>
        </div>
      </section>
    </main>
  );
}
