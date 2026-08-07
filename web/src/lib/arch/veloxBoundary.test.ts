/**
 * Architecture guard — InstaEdit browser never reaches Velox.
 *
 * Negative-requirement pin (Azione 2 del piano di disaccoppiamento):
 * le schermate Groups / Channels / Group detail / YouTube videos /
 * selezione canale / lingua canale devono usare SOLO InstaEdit API →
 * InstaEdit DB. Il browser non deve mai effettuare richieste verso
 * Velox, né direttamente né tramite i BFF /api/v1/velox/* o
 * /integrations/velox/*.
 *
 * Questa guardia è statica: scandisce l'intero albero `web/src` e
 * fallisce (fail-closed) se compare uno dei pattern proibiti nei
 * sorgenti .ts/.tsx. È un vincolo negativo — chi reintroduce una
 * chiamata Velox in una schermata InstaEdit rompe la suite, non la
 * produzione.
 */
import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const SRC_ROOT = join(process.cwd(), "src");

/**
 * Pattern che indicherebbero che il bundle browser InstaEdit raggiunge
 * Velox. Tutti devono restare assenti da web/src.
 *
 * - VELOX_CONTROL_URL / VELOX_CONTROL_JWT_SECRET / VELOX_API_TOKEN:
 *   segreti/URL server-side, il browser non deve MAI leggerli.
 * - /api/v1/velox/: BFF user-facing che prossima al master Velox.
 * - /integrations/velox: endpoint destinations che parlano a Velox.
 */
const FORBIDDEN_PATTERNS: ReadonlyArray<string> = [
  "VELOX_CONTROL_URL",
  "VELOX_CONTROL_JWT_SECRET",
  "VELOX_API_TOKEN",
  "/api/v1/velox/",
  "integrations/velox",
];

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...walk(full));
    } else if (/\.(ts|tsx)$/.test(entry)) {
      // Il guard stesso contiene i pattern proibiti come letterali:
      // escludere questa cartella dalla scansione.
      if (!full.includes(`${join("src", "lib", "arch")}`)) {
        out.push(full);
      }
    }
  }
  return out;
}

describe("InstaEdit web — nessuna richiesta a Velox dal browser", () => {
  const files = walk(SRC_ROOT);

  it("web/src non contiene riferimenti a VELOX_CONTROL_URL o ai BFF Velox", () => {
    const offenders: Array<{ file: string; pattern: string }> = [];
    for (const file of files) {
      const content = readFileSync(file, "utf8");
      for (const pattern of FORBIDDEN_PATTERNS) {
        if (content.includes(pattern)) {
          offenders.push({ file, pattern });
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("le schermate Groups/Channels del piano esistono e sono coperte dalla guardia", () => {
    // File che rappresentano i flussi operativi del piano (Azione 2).
    // Se uno di questi file sparisse o venisse rinominato, il guard
    // deve restare valido; l'elenco documenta il perimetro coperto.
    const flowFiles = files.filter((f) =>
      /(Groups|GroupYouTubeVideos|GroupsDetailPanels|DashboardChannels|AccountDetails|ChannelsPerformance|YouTubeStudio|useGroupsData|useGroupYouTubeVideos|channelsApi|editorSessionsApi|YouTubeChannelsTray|LanguagePicker)\.(ts|tsx)$/.test(
        f,
      ),
    );
    expect(flowFiles.length).toBeGreaterThan(0);
  });
});
