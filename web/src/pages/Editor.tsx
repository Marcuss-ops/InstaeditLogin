import { Seo } from "../components/seo/Seo";
import { EditorNav } from "../components/editor/EditorNav";
import { EditorCanvas } from "../components/editor/EditorCanvas";
import { EditorVideoExamples } from "../components/editor/EditorVideoExamples";
import { EditorOutputs } from "../components/editor/EditorOutputs";
import { EditorSpeedStats } from "../components/editor/EditorSpeedStats";
import { EditorHowItWorks } from "../components/editor/EditorHowItWorks";
import { EditorTranslate } from "../components/editor/EditorTranslate";
import { EditorShortsCut } from "../components/editor/EditorShortsCut";
import { EditorStream } from "../components/editor/EditorStream";
import { EditorContact } from "../components/editor/EditorContact";
import { EditorFooter } from "../components/editor/EditorFooter";
import { useEditorState } from "../components/editor/useEditorState";

export function Editor() {
  useEditorState();
  return (
    <>
      <Seo title="InstaEdit — Editor" description="One raw idea, every platform." canonical="https://app.instaedit.org/editor" />
      <EditorNav />
      <main className="relative pt-16">
        <section className="relative pt-24 pb-20 overflow-hidden">
          <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
          <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
          <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-10 items-center min-h-[70vh]">
            <div className="lg:col-span-6 animate-fade-up">
              <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
                <span className="relative flex h-2 w-2">
                  <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
                </span>
                <span>AI editor · multi-platform · one click</span>
              </div>
              <h1 className="text-display-1 text-white">
                One raw idea.{" "}
                <span className="text-gradient-animated">Every platform.</span>
              </h1>
              <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch]">
                Upload your video once and InstaEdit rewrites, reframes and
                resizes it for YouTube, TikTok, Instagram, Facebook, LinkedIn,
                X and Threads — subtitles, thumbnails and chapters included.
              </p>
            </div>
            <div className="lg:col-span-6">
              <EditorCanvas />
            </div>
          </div>
        </section>
        <EditorVideoExamples />
        <EditorOutputs />
        <EditorSpeedStats />
        <EditorHowItWorks />
        <EditorTranslate />
        <EditorShortsCut />
        <EditorStream />
        <EditorContact />
      </main>
      <EditorFooter />
    </>
  );
}
