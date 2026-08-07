//go:build nvidiasmoke

package services

// Benchmark end-to-end della generazione/traduzione metadati NVIDIA.
//
// Eseguito SOLO on-demand con il build tag `nvidiasmoke`:
//
//	go test -tags=nvidiasmoke -v -run TestNVIDIAE2EBenchmark ./internal/services/
//
// Scenario minimo: 1 video, 3 lingue (it/en/es), dati semplici. Misura e
// confronta TRE strategie:
//
//	A) 1 richiesta = tutte le lingue   (design attuale del servizio)
//	B) N richieste sequenziali        (1 per lingua)
//	C) N richieste parallele          (1 per lingua, con semaforo)
//
// Alla fine scrive un report JSON (metriche per fase, per lingua, per
// strategia) in $NVIDIA_BENCH_OUT oppure /tmp/nvidia_benchmark_<ts>.json.
// Il test "10 lingue" (scaling) è opt-in: NVIDIA_BENCH_10=true.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const (
	benchSourceTitle = "Come iniziare a fare boxe"
	benchSourceDesc  = "Guida semplice per chi vuole iniziare ad allenarsi nella boxe."
	// Lingue dello scenario minimo (la sorgente è l'italiano).
	benchLangs = "it,en,es"
)

// benchLangResult è il risultato per singola lingua (titolo, descrizione,
// latenza, stato) — salvato anche nel report JSON.
type benchLangResult struct {
	Language          string `json:"language"`
	TranslatedTitle   string `json:"translated_title,omitempty"`
	TranslatedDesc    string `json:"translated_description,omitempty"`
	TranslationStatus string `json:"translation_status"` // success | failed | skipped
	LatencyMS         int64  `json:"latency_ms"`
	ErrorMessage      string `json:"error_message,omitempty"`
}

type benchStrategyResult struct {
	Strategy       string            `json:"strategy"` // all_in_one | sequential | parallel
	TotalLatencyMS int64             `json:"total_latency_ms"`
	RequestCount   int               `json:"request_count"`
	FailedCount    int               `json:"failed_count"`
	Languages      []benchLangResult `json:"languages"`
}

type benchReport struct {
	StartedAt       string                `json:"started_at"`
	FinishedAt      string                `json:"finished_at"`
	TotalDurationMS int64                 `json:"total_duration_ms"`
	NVIDIAModel     string                `json:"nvidia_model"`
	SourceTitle     string                `json:"source_title"`
	SourceDesc      string                `json:"source_description"`
	RequestCount    int                   `json:"nvidia_request_count"`
	Strategies      []benchStrategyResult `json:"strategies"`
}

// allLangsPrompt chiede UNA risposta con tutte le lingue richieste.
func allLangsPrompt(title, desc string, langs []string) string {
	return fmt.Sprintf(
		"Contenuto del video: %s\n\nDescrizione del video:\n%s\n\n"+
			"Genera titolo, descrizione, tag e traduzioni come da istruzioni di sistema. "+
			"IMPORTANTE: includi le traduzioni in TUTTE e solo queste lingue: %s. "+
			"default_language deve essere \"it\".",
		title, desc, strings.Join(langs, ", "),
	)
}

// oneLangPrompt chiede una SOLA traduzione nella lingua target.
func oneLangPrompt(title, desc, targetLang string) string {
	return fmt.Sprintf(
		"Contenuto del video: %s\n\nDescrizione del video:\n%s\n\n"+
			"Genera titolo, descrizione e traduzione come da istruzioni di sistema. "+
			"IMPORTANTE: default_language deve essere \"it\" e l'unica traduzione richiesta è %s.",
		title, desc, targetLang,
	)
}

// langsFor splits the benchLangs list.
func langsFor() []string {
	out := []string{}
	for _, l := range strings.Split(benchLangs, ",") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// findTranslation recupera la traduzione per lingua (case-insensitive:
// la validazione normalizza le chiavi in lowercase).
func findTranslation(meta *NVIDIAMetadataResponse, lang string) (models.YouTubeTranslation, bool) {
	if meta == nil || meta.Translations == nil {
		return models.YouTubeTranslation{}, false
	}
	if tr, ok := meta.Translations[lang]; ok {
		return tr, true
	}
	if tr, ok := meta.Translations[strings.ToLower(lang)]; ok {
		return tr, true
	}
	return models.YouTubeTranslation{}, false
}

// runSingleLang genera una sola traduzione e misura la latenza.
func runSingleLang(ctx context.Context, g *MetadataGenerator, lang string) benchLangResult {
	start := time.Now()
	meta, err := g.Generate(ctx, oneLangPrompt(benchSourceTitle, benchSourceDesc, lang))
	latency := time.Since(start).Milliseconds()
	res := benchLangResult{Language: lang, TranslationStatus: "failed", LatencyMS: latency}
	if err != nil {
		res.ErrorMessage = err.Error()
		return res
	}
	tr, ok := findTranslation(meta, lang)
	if !ok {
		// Se il modello non restituisce la chiave richiesta, il
		// default del JSON (meta.Title/Description) è comunque utile
		// solo per la lingua sorgente.
		res.ErrorMessage = "traduzione richiesta non presente nella risposta"
		return res
	}
	res.TranslatedTitle = tr.Title
	res.TranslatedDesc = tr.Description
	res.TranslationStatus = "success"
	return res
}

// strategyAllInOne: 1 richiesta = tutte le lingue.
func strategyAllInOne(ctx context.Context, g *MetadataGenerator) benchStrategyResult {
	langs := langsFor()
	start := time.Now()
	meta, err := g.Generate(ctx, allLangsPrompt(benchSourceTitle, benchSourceDesc, langs))
	res := benchStrategyResult{Strategy: "all_in_one", TotalLatencyMS: time.Since(start).Milliseconds(), RequestCount: 1}
	if err != nil {
		res.FailedCount = len(langs)
		for _, l := range langs {
			res.Languages = append(res.Languages, benchLangResult{Language: l, TranslationStatus: "failed", ErrorMessage: err.Error()})
		}
		return res
	}
	// Lingua sorgente = default del JSON.
	res.Languages = append(res.Languages, benchLangResult{
		Language:          "it",
		TranslationStatus: "skipped", // è la sorgente
		TranslatedTitle:   meta.Title,
		TranslatedDesc:    meta.Description,
	})
	for _, l := range langs {
		if l == "it" {
			continue
		}
		tr, ok := findTranslation(meta, l)
		if !ok {
			res.FailedCount++
			res.Languages = append(res.Languages, benchLangResult{Language: l, TranslationStatus: "failed", ErrorMessage: "lingua mancante nella risposta"})
			continue
		}
		res.Languages = append(res.Languages, benchLangResult{
			Language:          l,
			TranslationStatus: "success",
			TranslatedTitle:   tr.Title,
			TranslatedDesc:    tr.Description,
		})
	}
	return res
}

// strategySequential: 1 richiesta per lingua, una dopo l'altra.
func strategySequential(ctx context.Context, g *MetadataGenerator) benchStrategyResult {
	langs := langsFor()
	start := time.Now()
	res := benchStrategyResult{Strategy: "sequential", RequestCount: len(langs)}
	for _, l := range langs {
		if l == "it" {
			continue
		}
		r := runSingleLang(ctx, g, l)
		if r.TranslationStatus != "success" {
			res.FailedCount++
		}
		res.Languages = append(res.Languages, r)
	}
	res.TotalLatencyMS = time.Since(start).Milliseconds()
	return res
}

// strategyParallel: 1 richiesta per lingua, in parallelo con semaforo.
func strategyParallel(ctx context.Context, g *MetadataGenerator, concurrency int) benchStrategyResult {
	langs := langsFor()
	targets := []string{}
	for _, l := range langs {
		if l != "it" {
			targets = append(targets, l)
		}
	}
	start := time.Now()
	sem := make(chan struct{}, concurrency)
	results := make([]benchLangResult, len(targets))
	var wg sync.WaitGroup
	for i, l := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, lang string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runSingleLang(ctx, g, lang)
		}(i, l)
	}
	wg.Wait()
	res := benchStrategyResult{Strategy: "parallel", RequestCount: len(targets), TotalLatencyMS: time.Since(start).Milliseconds()}
	for _, r := range results {
		if r.TranslationStatus != "success" {
			res.FailedCount++
		}
		res.Languages = append(res.Languages, r)
	}
	return res
}

// TestNVIDIAE2EBenchmark è il benchmark principale (scenario minimo, 3 lingue).
func TestNVIDIAE2EBenchmark(t *testing.T) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY non impostata — salto il benchmark live")
	}
	model := os.Getenv("NVIDIA_MODEL")
	if model == "" {
		t.Logf("⚠ NVIDIA_MODEL non impostata: uso il default %q (potrebbe dare 404 per questo account)", defaultNVIDIAModel)
	}
	g := NewMetadataGenerator(apiKey, WithModel(model))
	ctx := context.Background()

	report := benchReport{
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		NVIDIAModel: model,
		SourceTitle: benchSourceTitle,
		SourceDesc:  benchSourceDesc,
	}

	t.Logf("── Benchmark NVIDIA (scenario minimo, 3 lingue: %s) ──", benchLangs)
	t.Logf("Titolo sorgente: %q", benchSourceTitle)
	t.Logf("Descrizione sorgente: %q", benchSourceDesc)
	t.Logf("Modello: %q", model)

	// Strategia A — 1 richiesta = tutte le lingue.
	t.Log("\n▸ STRATEGIA A — 1 richiesta = tutte le lingue (design attuale)")
	a := strategyAllInOne(ctx, g)
	report.Strategies = append(report.Strategies, a)
	report.RequestCount += a.RequestCount
	t.Logf("  totale: %dms  (1 richiesta)", a.TotalLatencyMS)
	for _, l := range a.Languages {
		t.Logf("  %-4s %-8s %-55s %s", l.Language, l.TranslationStatus, truncate(l.TranslatedTitle, 55), truncate(l.TranslatedDesc, 45))
		if l.ErrorMessage != "" {
			t.Logf("       ⚠ %s", l.ErrorMessage)
		}
	}

	// Strategia B — N richieste sequenziali (1 per lingua).
	t.Log("\n▸ STRATEGIA B — richieste sequenziali (1 per lingua)")
	b := strategySequential(ctx, g)
	report.Strategies = append(report.Strategies, b)
	report.RequestCount += b.RequestCount
	t.Logf("  totale: %dms  (%d richieste)", b.TotalLatencyMS, b.RequestCount)
	for _, l := range b.Languages {
		t.Logf("  %-4s %-8s %-55s %s  [%dms]", l.Language, l.TranslationStatus, truncate(l.TranslatedTitle, 55), truncate(l.TranslatedDesc, 45), l.LatencyMS)
		if l.ErrorMessage != "" {
			t.Logf("       ⚠ %s", l.ErrorMessage)
		}
	}

	// Strategia C — richieste parallele (1 per lingua, semaforo=3).
	t.Log("\n▸ STRATEGIA C — richieste parallele (1 per lingua, concorrenza 3)")
	c := strategyParallel(ctx, g, 3)
	report.Strategies = append(report.Strategies, c)
	report.RequestCount += c.RequestCount
	t.Logf("  totale (wall clock): %dms  (%d richieste)", c.TotalLatencyMS, c.RequestCount)
	for _, l := range c.Languages {
		t.Logf("  %-4s %-8s %-55s %s  [%dms]", l.Language, l.TranslationStatus, truncate(l.TranslatedTitle, 55), truncate(l.TranslatedDesc, 45), l.LatencyMS)
		if l.ErrorMessage != "" {
			t.Logf("       ⚠ %s", l.ErrorMessage)
		}
	}

	report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	report.TotalDurationMS = time.Since(mustParseTime(report.StartedAt)).Milliseconds()

	// Report JSON.
	path := os.Getenv("NVIDIA_BENCH_OUT")
	if path == "" {
		path = fmt.Sprintf("/tmp/nvidia_benchmark_%d.json", time.Now().Unix())
	}
	if err := writeBenchReport(path, report); err != nil {
		t.Logf("⚠ impossibile scrivere il report: %v", err)
	} else {
		t.Logf("\nReport JSON salvato in: %s", path)
	}

	t.Logf("\n── Sintesi ──")
	t.Logf("A (tutte le lingue in 1 richiesta): %dms", a.TotalLatencyMS)
	t.Logf("B (sequenziale, 1 per lingua):      %dms", b.TotalLatencyMS)
	t.Logf("C (parallelo, 1 per lingua):        %dms", c.TotalLatencyMS)
	if b.TotalLatencyMS > 0 && c.TotalLatencyMS > 0 {
		t.Logf("Speedup parallelo vs sequenziale: %.2fx", float64(b.TotalLatencyMS)/float64(c.TotalLatencyMS))
	}
}

// TestNVIDIAE2EBenchmark10 è opt-in (NVIDIA_BENCH_10=true): 10 lingue,
// strategia "1 richiesta = tutte le lingue" (scaling).
func TestNVIDIAE2EBenchmark10(t *testing.T) {
	if os.Getenv("NVIDIA_BENCH_10") != "true" {
		t.Skip("NVIDIA_BENCH_10 != true — salto il test 10 lingue (opt-in, richiede ~2-3 min)")
	}
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY non impostata")
	}
	model := os.Getenv("NVIDIA_MODEL")
	g := NewMetadataGenerator(apiKey, WithModel(model))
	langs := []string{"it", "en", "es", "fr", "de", "pt-BR", "ja", "ko", "ar", "hi", "ru"}
	start := time.Now()
	meta, err := g.Generate(context.Background(), allLangsPrompt(benchSourceTitle, benchSourceDesc, langs))
	elapsed := time.Since(start)
	t.Logf("10 lingue — 1 richiesta: %s", elapsed.Round(time.Millisecond))
	if err != nil {
		t.Fatalf("GENERATE ERRORE: %v", err)
	}
	got := len(meta.Translations)
	t.Logf("traduzioni ricevute: %d su %d", got, len(langs)-1)
	for _, l := range langs {
		if l == "it" {
			continue
		}
		if tr, ok := findTranslation(meta, l); ok {
			t.Logf("  ✓ %-6s %-60s %s", l, truncate(tr.Title, 60), truncate(tr.Description, 45))
		} else {
			t.Logf("  ✗ %-6s MANCANTE", l)
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func mustParseTime(s string) time.Time {
	tt, _ := time.Parse(time.RFC3339, s)
	return tt
}

func writeBenchReport(path string, r benchReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
