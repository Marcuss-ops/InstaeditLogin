//go:build nvidiasmoke

package services

// Smoke test per la generazione/traduzione metadati NVIDIA.
//
// Eseguito SOLO on-demand con il build tag `nvidiasmoke`:
//
//	go test -tags=nvidiasmoke -v -run TestNVIDIA ./internal/services/
//
// Contenuto:
//  1. TestNVIDIATranslationsLive — chiamata REALE a integrate.api.nvidia.com
//     con 10 lingue richieste, titolo casuale e descrizione lunga; misura
//     il tempo totale e stampa cosa ha restituito il modello.
//  2. Casi d'errore deterministici (httptest locale, nessuna rete):
//     - chiave vuota            → ErrNVIDIANotConfigured
//     - HTTP 500 da NVIDIA      → errore "nvidia returned HTTP 500"
//     - JSON di chat corrotto   → errore di parse
//     - descrizione > 5000      → ErrNVIDIAResponseInvalid
//     - chiave lingua non BCP47 → ErrNVIDIAResponseInvalid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// writeChatResponse scrive una risposta chat completions valida con il
// contenuto dato (già JSON) nel campo message.content.
func writeChatResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
}

// translationTargets sono le 10 lingue richieste nel test live.
var translationTargets = []string{"en", "es", "fr", "de", "pt-BR", "ja", "ko", "ar", "hi", "ru"}

// randomTitle restituisce un titolo plausibile con un suffisso casuale.
func randomTitle() string {
	topics := []string{
		"Il segreto per coltivare pomodori perfetti in balcone",
		"Come imparare lo spagnolo in 30 giorni senza annoiarsi",
		"La guida definitiva al home office produttivo",
		"Perché il tuo gatto ti ignora (e come rimediare)",
		"Ricette vegane veloci per studenti universitari",
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%s — Episodio %03d", topics[r.Intn(len(topics))], r.Intn(900)+100)
}

// longDescription costruisce una descrizione "lunghetta" (~1200 caratteri).
func longDescription(topic string) string {
	base := fmt.Sprintf(
		"In questo video parliamo di %s. Vediamo passo dopo passo tutti i dettagli: "+
			"strumenti necessari, errori da evitare, tempi e costi, e i risultati che puoi ottenere "+
			"già dalla prima settimana. Il canale nasce per chi vuole risultati concreti senza "+
			"sprecare tempo: ogni puntata è autonoma, con esempi pratici e FAQ finali.",
		topic)
	// Allunga la descrizione ripetendo il corpo con variazioni fino a ~1200 rune.
	for len([]rune(base)) < 1200 {
		base += " " + base
	}
	runes := []rune(base)
	if len(runes) > 1250 {
		runes = runes[:1250]
	}
	return string(runes)
}

// TestNVIDIATranslationsLive chiama la VERA API NVIDIA e misura il tempo.
func TestNVIDIATranslationsLive(t *testing.T) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY non impostata nell'ambiente — salto il test live")
	}

	title := randomTitle()
	desc := longDescription(title)
	prompt := fmt.Sprintf(
		"Contenuto del video: %s\n\nDescrizione del video:\n%s\n\n"+
			"Genera titolo, descrizione, tag e traduzioni come da istruzioni di sistema. "+
			"IMPORTANTE: includi le traduzioni in TUTTE e solo queste 10 lingue: %s. "+
			"default_language deve essere \"it\".",
		title, desc, strings.Join(translationTargets, ", "),
	)

	model := os.Getenv("NVIDIA_MODEL") // "" → default del servizio
	if model == "" {
		// Il default (nvidia/llama-3.1-nemotron-70b-instruct) non è
		// accessibile per tutti gli account (404 "Function not found
		// for account"): se non è impostato NVIDIA_MODEL il test può
		// fallire per questo motivo, non perché il flusso sia rotto.
		t.Logf("⚠ NVIDIA_MODEL non impostata — uso il default %q (se fallisce con 404, imposta NVIDIA_MODEL su un modello accessibile dal tuo account)", defaultNVIDIAModel)
	}
	t.Logf("─ Prompt di test ──────────────────────────────")
	t.Logf("Modello: %q", model)
	t.Logf("Titolo chiesto: %q", title)
	t.Logf("Descrizione: %d caratteri (rune)", len([]rune(desc)))
	t.Logf("Lingue richieste: %d (%s)", len(translationTargets), strings.Join(translationTargets, ", "))
	t.Logf("───────────────────────────────────────────────")

	g := NewMetadataGenerator(apiKey, WithModel(model))
	start := time.Now()
	meta, err := g.Generate(context.Background(), prompt)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GENERATE ERRORE dopo %s: %v", elapsed, err)
	}

	t.Logf("✓ GENERATE OK in %s", elapsed.Round(time.Millisecond))
	t.Logf("─ Risultato ───────────────────────────────────")
	t.Logf("title (%d/100): %q", len([]rune(meta.Title)), meta.Title)
	t.Logf("description: %d/5000 caratteri", len([]rune(meta.Description)))
	t.Logf("tags: %d", len(meta.Tags))
	t.Logf("default_language: %q  default_audio_language: %q", meta.DefaultLanguage, meta.DefaultAudioLanguage)
	t.Logf("traduzioni ricevute: %d su %d richieste", len(meta.Translations), len(translationTargets))

	// Controllo per-lingua: esistenza, lunghezze, e se la traduzione è
	// davvero una traduzione (titolo diverso da quello di default).
	// Nota: la validazione server-side normalizza le chiavi in lowercase
	// (pt-BR → pt-br), quindi il lookup è case-insensitive.
	identical := 0
	for _, lang := range translationTargets {
		tr, ok := meta.Translations[lang]
		if !ok {
			tr, ok = meta.Translations[strings.ToLower(lang)]
		}
		if !ok {
			t.Logf("  ✗ %-6s MANCANTE (il modello non l'ha generata)", lang)
			continue
		}
		same := tr.Title == meta.Title
		if same {
			identical++
		}
		t.Logf("  ✓ %-6s title=%dch desc=%dch %s",
			lang, len([]rune(tr.Title)), len([]rune(tr.Description)),
			map[bool]string{true: "[⚠ identico al default]", false: ""}[same])
	}
	t.Logf("───────────────────────────────────────────────")
	t.Logf("Lingue tradotte diversamente dal default: %d/%d", len(meta.Translations)-identical, len(translationTargets))

	// Asserzioni di base (già garantite dalla validazione server-side).
	if meta.Title == "" {
		t.Error("title vuoto")
	}
	if len([]rune(meta.Title)) > 100 {
		t.Errorf("title %d caratteri > 100", len([]rune(meta.Title)))
	}
	if len([]rune(meta.Description)) > 5000 {
		t.Errorf("description %d caratteri > 5000", len([]rune(meta.Description)))
	}
	if len(meta.Translations) == 0 {
		t.Error("nessuna traduzione generata")
	}
}

// TestNVIDIATranslateLive chiama la VERA API NVIDIA per il passo di
// traduzione per-lingua (posting per canale): 1 richiesta = 1 lingua,
// misura la latenza di ogni lingua.
func TestNVIDIATranslateLive(t *testing.T) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY non impostata nell'ambiente — salto il test live")
	}

	model := os.Getenv("NVIDIA_MODEL")
	g := NewMetadataGenerator(apiKey, WithModel(model))
	title := "Come iniziare a fare boxe"
	desc := "Guida semplice per chi vuole iniziare ad allenarsi nella boxe: strumenti, errori da evitare e primi passi."

	for _, lang := range []string{"en", "es", "de"} {
		start := time.Now()
		tr, err := g.Translate(context.Background(), TranslateRequest{
			Title:          title,
			Description:    desc,
			SourceLanguage: "it",
			TargetLanguage: lang,
		})
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("TRANSLATE %s ERRORE dopo %s: %v", lang, elapsed, err)
			continue
		}
		if tr.Title == "" || tr.Description == "" {
			t.Errorf("TRANSLATE %s: campi vuoti (title=%q desc=%q)", lang, tr.Title, tr.Description)
		}
		if tr.Title == title {
			t.Errorf("TRANSLATE %s: titolo identico all'originale (non tradotto)", lang)
		}
		t.Logf("✓ %-3s in %s — title=%q", lang, elapsed.Round(time.Millisecond), tr.Title)
	}
}

// TestNVIDIATranslateNotConfigured: chiave vuota → ErrNVIDIANotConfigured.
func TestNVIDIATranslateNotConfigured(t *testing.T) {
	g := NewMetadataGenerator("")
	_, err := g.Translate(context.Background(), TranslateRequest{TargetLanguage: "es"})
	if !errors.Is(err, ErrNVIDIANotConfigured) {
		t.Fatalf("atteso ErrNVIDIANotConfigured, ottenuto: %v", err)
	}
	t.Log("✓ Translate chiave vuota → ErrNVIDIANotConfigured")
}

// TestNVIDIATranslateEmptyTarget: lingua target vuota → errore PRIMA
// della chiamata HTTP (la guardia CheckBCP47Like lascerebbe passare
// la stringa vuota).
func TestNVIDIATranslateEmptyTarget(t *testing.T) {
	g := NewMetadataGenerator("fake-key")
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title: "Titolo", Description: "Descrizione", TargetLanguage: "",
	})
	if err == nil {
		t.Fatal("atteso errore su lingua target vuota, ottenuto nil")
	}
	t.Logf("✓ lingua target vuota → %v", err)
}

// TestNVIDIATranslateIdenticalOutput: il modello rimanda indietro il
// testo sorgente (titolo E descrizione identici) → ErrNVIDIAResponseInvalid
// (il worker NON pubblica la lingua sbagliata).
func TestNVIDIATranslateIdenticalOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{
			"title":       "Come iniziare a fare boxe",
			"description": "Guida semplice per iniziare.",
		})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title:          "Come iniziare a fare boxe",
		Description:    "Guida semplice per iniziare.",
		SourceLanguage: "it",
		TargetLanguage: "es",
	})
	if !errors.Is(err, ErrNVIDIAResponseInvalid) {
		t.Fatalf("atteso ErrNVIDIAResponseInvalid su output identico, ottenuto: %v", err)
	}
	t.Logf("✓ output identico all'originale → %v", err)
}

// TestNVIDIATranslateBadTargetLanguage: lingua target non BCP-47 →
// errore PRIMA della chiamata HTTP.
func TestNVIDIATranslateBadTargetLanguage(t *testing.T) {
	g := NewMetadataGenerator("fake-key")
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title:          "Titolo",
		Description:    "Descrizione",
		TargetLanguage: "not a lang!",
	})
	if err == nil {
		t.Fatal("atteso errore su lingua target malformata, ottenuto nil")
	}
	if !strings.Contains(err.Error(), "target_language") {
		t.Fatalf("l'errore deve citare target_language, ottenuto: %v", err)
	}
	t.Logf("✓ lingua target non BCP-47 → %v", err)
}

// TestNVIDIATranslateEmptyInput: titolo E descrizione entrambi vuoti →
// errore PRIMA della chiamata HTTP.
func TestNVIDIATranslateEmptyInput(t *testing.T) {
	g := NewMetadataGenerator("fake-key")
	_, err := g.Translate(context.Background(), TranslateRequest{TargetLanguage: "es"})
	if err == nil {
		t.Fatal("atteso errore su input vuoto, ottenuto nil")
	}
	t.Logf("✓ input vuoto → %v", err)
}

// TestNVIDIATranslateSuccess: risposta valida → traduzione restituita
// e la richiesta HTTP contiene la lingua target.
func TestNVIDIATranslateSuccess(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		content, _ := json.Marshal(map[string]any{
			"title":       "Cómo empezar a practicar boxeo",
			"description": "Una guía sencilla para empezar.",
		})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	tr, err := g.Translate(context.Background(), TranslateRequest{
		Title:          "Come iniziare a fare boxe",
		Description:    "Guida semplice.",
		SourceLanguage: "it",
		TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if tr.Title != "Cómo empezar a practicar boxeo" {
		t.Errorf("title: want tradotto, got %q", tr.Title)
	}
	if tr.Description != "Una guía sencilla para empezar." {
		t.Errorf("description: want tradotto, got %q", tr.Description)
	}
	// La lingua target appare nel prompt utente come "Target language: es"
	// (nel JSON trasportato le virgolette sono escaped, quindi matchiamo
	// la sequenza senza virgolette).
	if !strings.Contains(gotBody, "Target language: es") {
		t.Errorf("la richiesta deve citare la lingua target es: %s", gotBody)
	}
	t.Logf("✓ Translate success → %q", tr.Title)
}

// TestNVIDIATranslateServerError: NVIDIA risponde HTTP 500 → errore
// propagato (il worker marca il target failed + retry).
func TestNVIDIATranslateServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream boom"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title: "Titolo", Description: "Descrizione", TargetLanguage: "es",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("atteso errore HTTP 500, ottenuto: %v", err)
	}
	t.Logf("✓ Translate HTTP 500 → %v", err)
}

// TestNVIDIATranslateEmptyResponse: il modello risponde con JSON vuoto
// → ErrNVIDIAResponseInvalid (il worker NON pubblica la lingua sbagliata).
func TestNVIDIATranslateEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{"title": "", "description": ""})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title: "Titolo", Description: "Descrizione", TargetLanguage: "es",
	})
	if !errors.Is(err, ErrNVIDIAResponseInvalid) {
		t.Fatalf("atteso ErrNVIDIAResponseInvalid, ottenuto: %v", err)
	}
	t.Logf("✓ Translate risposta vuota → %v", err)
}

// TestNVIDIATranslateOversizedOutput: descrizione tradotta oltre i 5000
// caratteri → ErrNVIDIAResponseInvalid.
func TestNVIDIATranslateOversizedOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{
			"title": "Titolo", "description": strings.Repeat("a", 5001),
		})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Translate(context.Background(), TranslateRequest{
		Title: "Titolo", Description: "Descrizione", TargetLanguage: "es",
	})
	if !errors.Is(err, ErrNVIDIAResponseInvalid) {
		t.Fatalf("atteso ErrNVIDIAResponseInvalid, ottenuto: %v", err)
	}
	t.Logf("✓ Translate output > 5000 → %v", err)
}

// TestNVIDIANotConfigured: chiave vuota → errore atteso.
func TestNVIDIANotConfigured(t *testing.T) {
	g := NewMetadataGenerator("")
	_, err := g.Generate(context.Background(), "qualsiasi prompt")
	if !errors.Is(err, ErrNVIDIANotConfigured) {
		t.Fatalf("atteso ErrNVIDIANotConfigured, ottenuto: %v", err)
	}
	t.Log("✓ chiave vuota → ErrNVIDIANotConfigured (endpoint risponde 503)")
}

// TestNVIDIAServerError: NVIDIA risponde HTTP 500 → errore propagato.
func TestNVIDIAServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream boom"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Generate(context.Background(), "x")
	if err == nil {
		t.Fatal("atteso errore su HTTP 500, ottenuto nil")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("l'errore deve citare HTTP 500, ottenuto: %v", err)
	}
	t.Logf("✓ HTTP 500 da NVIDIA → %v", err)
}

// TestNVIDIAInvalidChatJSON: risposta chat corrotta → errore di parse.
func TestNVIDIAInvalidChatJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":`) // JSON troncato
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Generate(context.Background(), "x")
	if err == nil {
		t.Fatal("atteso errore su JSON corrotto, ottenuto nil")
	}
	t.Logf("✓ JSON chat corrotto → %v", err)
}

// TestNVIDIAOversizedDescription: il modello restituisce una descrizione
// oltre i 5000 caratteri → ErrNVIDIAResponseInvalid.
func TestNVIDIAOversizedDescription(t *testing.T) {
	big := strings.Repeat("a", 5001)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{
			"title":                  "Titolo",
			"description":            big,
			"tags":                   []string{},
			"default_language":       "it",
			"default_audio_language": "it",
			"translations":           map[string]any{},
		})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Generate(context.Background(), "x")
	if !errors.Is(err, ErrNVIDIAResponseInvalid) {
		t.Fatalf("atteso ErrNVIDIAResponseInvalid, ottenuto: %v", err)
	}
	t.Logf("✓ descrizione > 5000 caratteri → %v", err)
}

// TestNVIDIABadLanguageKey: chiave lingua non BCP-47 → ErrNVIDIAResponseInvalid.
func TestNVIDIABadLanguageKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(map[string]any{
			"title":            "Titolo",
			"description":      "Descrizione",
			"tags":             []string{},
			"default_language": "it",
			"translations": map[string]any{
				"not a lang!": map[string]any{"title": "X", "description": "Y"},
			},
		})
		writeChatResponse(w, string(content))
	}))
	defer srv.Close()

	g := NewMetadataGenerator("fake-key")
	g.apiURL = srv.URL
	_, err := g.Generate(context.Background(), "x")
	if !errors.Is(err, ErrNVIDIAResponseInvalid) {
		t.Fatalf("atteso ErrNVIDIAResponseInvalid, ottenuto: %v", err)
	}
	t.Logf("✓ chiave lingua non BCP-47 → %v", err)
}
