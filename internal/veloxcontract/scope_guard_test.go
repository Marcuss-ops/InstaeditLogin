package veloxcontract

import (
	"reflect"
	"strings"
	"testing"
)

// Guardia architetturale — Azione 2 del disaccoppiamento InstaEdit↔Velox.
//
// Invariante negativo: il contratto client InstaEdit→Velox DEVE essere
// limitato alle operazioni editor/render (jobs, workers, assets, editor
// bridge). Nessun metodo che enumera/gestisce gruppi, canali, account o
// video di catalogo può esistere qui: se un handler Groups/Channels avesse
// bisogno di Velox, il primo sintomo sarebbe un metodo nuovo su questa
// interfaccia. Questo test fallisce closed quando ciò accade.
//
// Il companion test lato client (internal/veloxclient) verifica che
// l'implementazione concreta rispetti la stessa superficie. Il test che
// gli schermi InstaEdit non chiamino mai /api/v1/velox/* vive nel repo
// frontend (web/src/lib/arch/veloxBoundary.test.ts).
func TestClientInterface_HasNoGroupChannelOperations(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil)).Elem()

	// Metodi che un catalogo operativo richiederebbe. Se qualcuno li
	// aggiunge, il disaccoppiamento è regredito e il piano (Test A/B/C)
	// fallirebbe.
	forbiddenPrefixes := []string{"List", "Get", "Create", "Update", "Delete", "Resolve", "Sync", "Mirror"}
	forbiddenDomain := []string{"Group", "Channel", "Account", "Video", "Membership", "Workspace", "Catalog"}

	for i := 0; i < clientType.NumMethod(); i++ {
		method := clientType.Method(i)
		for _, domain := range forbiddenDomain {
			if strings.Contains(method.Name, domain) {
				t.Errorf(
					"Client.%s: il contratto InstaEdit→Velox non deve esporre operazioni di dominio %q (gruppi/canali/account/video appartengono a InstaEdit DB)",
					method.Name, domain,
				)
			}
		}
		for _, prefix := range forbiddenPrefixes {
			if strings.HasPrefix(method.Name, prefix) && isCatalogCandidate(method.Name) {
				t.Errorf(
					"Client.%s: nessun metodo di enumerazione operativa sul catalogo (i dati Groups/Channels vivono solo in InstaEdit DB)",
					method.Name,
				)
			}
		}
	}
}

// isCatalogCandidate is true for enumeration-style names whose subject
// is a plural catalog noun (Jobs/Workers/Assets/Deliveries are allowed:
// sono dominio render). Conservatrice: ammette solo i soggetti noti del
// contratto render.
func isCatalogCandidate(methodName string) bool {
	switch {
	case strings.Contains(methodName, "Job"),
		strings.Contains(methodName, "Worker"),
		strings.Contains(methodName, "Asset"),
		strings.Contains(methodName, "Delivery"),
		strings.Contains(methodName, "Proxy"),
		strings.Contains(methodName, "Editor"):
		return false
	}
	return true
}

// TestClientInterface_AllowedMethods pins l'elenco esatto dei metodi del
// contratto render. Aggiungere un metodo per operazioni di catalogo qui
// rompe il test — ed è esattamente ciò che vogliamo.
func TestClientInterface_AllowedMethods(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil)).Elem()

	allowed := map[string]bool{
		"ListJobs":          true,
		"CreateJob":         true,
		"GetJob":            true,
		"CancelJob":         true,
		"ListJobDeliveries": true,
		"ListWorkers":       true,
		"GetWorker":         true,
		"GetAsset":          true,
	}
	got := map[string]bool{}
	for i := 0; i < clientType.NumMethod(); i++ {
		name := clientType.Method(i).Name
		got[name] = true
		if !allowed[name] {
			t.Errorf("Client.%s: metodo non previsto nel contratto render; se serve un'operazione editor esterna aggiungerla SOLO come bridge project-scoped, mai come catalogo", name)
		}
	}
	for name := range allowed {
		if !got[name] {
			t.Errorf("Client.%s: metodo atteso dal contratto mancante", name)
		}
	}
}

// TestScopeTaxonomy_NoCatalogScopes pinna che la tassonomia dei control
// JWT resta limitata a jobs/workers/assets/editor. Uno scope di catalogo
// (groups.read, channels.read, …) significherebbe che Velox autorizza
// letture di dati che non possiede.
func TestScopeTaxonomy_NoCatalogScopes(t *testing.T) {
	scopes := []string{
		ScopeVeloxJobsRead,
		ScopeVeloxJobsWrite,
		ScopeVeloxWorkersRead,
		ScopeVeloxAssetsRead,
		ScopeVeloxAssetsWrite,
		ScopeVeloxEditorRead,
		ScopeVeloxEditorWrite,
	}
	for _, s := range scopes {
		if s == "" {
			t.Fatal("scope vuoto nella tassonomia")
		}
		for _, domain := range []string{"group", "channel", "account", "video", "catalog", "workspace"} {
			if strings.Contains(strings.ToLower(s), domain) {
				t.Errorf("scope %q: scope di catalogo proibito (gruppi/canali/video vivono solo in InstaEdit DB)", s)
			}
		}
	}
}
