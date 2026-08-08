/**
 * Architecture guard — il bundle InstaEdit non contiene mai link file://.
 *
 * Vincolo negativo: Chrome emette il warning "i contenuti in … non possono
 * caricare o avere link che rimandino a file:///" quando una pagina espone
 * ancore/iframe/risorse che puntano a file:// — un rischio di sicurezza
 * reale (fuga di percorsi locali) e una fonte di falsi positivi in console.
 *
 * Questa guardia è statica e fail-closed:
 *   - scandisce l'intero albero `web/src` (.ts/.tsx) → nessun letterale
 *     file:// nei sorgenti;
 *   - scandisce il bundle compilato `web/dist` (.js/.css/.html) quando
 *     esiste → nessun letterale file:// nell'output di produzione.
 *
 * Chi reintroduce un link file:// (in un href, in window.open, in un src
 * di iframe/img) rompe la suite, non la produzione. La difesa runtime resta
 * comunque in editorSessionsApi.ts (validateEditorURL rifiuta ogni
 * protocollo non http/https): questa guardia intercetta il caso statico
 * alla radice.
 */
import { describe, expect, it } from "vitest";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const WEB_ROOT = process.cwd();
const SRC_ROOT = join(WEB_ROOT, "src");
const DIST_ROOT = join(WEB_ROOT, "dist");

/**
 * Letterale proibito: uno schema URL file://. Qualsiasi occorrenza nei
 * sorgenti o nel bundle significa che la SPA può mostrare/ navigare verso
 * risorse locali del visitatore.
 */
const FILE_SCHEME = "file://";

/**
 * Sorgenti .ts/.tsx che finiscono nel bundle di produzione.
 *
 * Esclusioni:
 *   - la directory della guardia stessa (contiene il pattern come
 *     letterale, come veloxBoundary);
 *   - i file *.test.* — non entrano nel bundle, e i test di regressione
 *     della difesa runtime (es. editorSessionsApi.test.ts) citano
 *     legittimamente "file:///etc/passwd" come input proibito.
 */
function walkSource(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...walkSource(full));
    } else if (/\.(ts|tsx)$/.test(entry) && !/\.test\.(ts|tsx)$/.test(entry)) {
      if (!full.includes(join("src", "lib", "arch"))) {
        out.push(full);
      }
    }
  }
  return out;
}

/** Bundle compilato (.js/.css/.html — la parte che arriva al browser). */
function walkBundle(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...walkBundle(full));
    } else if (/\.(js|css|html)$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

function findFileScheme(files: string[]): Array<{ file: string }> {
  const offenders: Array<{ file: string }> = [];
  for (const file of files) {
    const content = readFileSync(file, "utf8");
    if (content.includes(FILE_SCHEME)) {
      offenders.push({ file });
    }
  }
  return offenders;
}

describe("InstaEdit web — nessun link file://", () => {
  it("web/src non contiene letterali file:// nei sorgenti .ts/.tsx", () => {
    const files = walkSource(SRC_ROOT);
    expect(files.length).toBeGreaterThan(0);
    expect(findFileScheme(files)).toEqual([]);
  });

  it("web/dist (bundle di produzione) non contiene file:// quando è presente", () => {
    // Il bundle non esiste in un checkout fresco / prima della build:
    // il guard si applica SOLO quando dist/ è stato generato (così la
    // suite resta verde in CI senza build, ma blocca un bundle sporco
    // quando qualcuno fa build localmente o in preflight).
    if (!existsSync(DIST_ROOT)) {
      return;
    }
    const files = walkBundle(DIST_ROOT);
    expect(files.length).toBeGreaterThan(0);
    expect(findFileScheme(files)).toEqual([]);
  });
});
