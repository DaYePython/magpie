package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

func TestSharedProviderReplacesPortableSettings(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"review-model"}]}`))
	}))
	defer vendor.Close()
	existing := provider.Provider{ID: "review-relay", Name: "Review Relay", Chat: vendor.URL + "/v1", Key: "review-primary", NoModels: true, PriceRate: 0.5, Routing: "weight", RefreshEvery: "1h", AutoInclude: []string{"old-*"}, ExtraBody: provider.ExtraBody{"temperature": json.RawMessage(`0.1`)}}
	if err := provider.Save(existing); err != nil {
		t.Fatal(err)
	}
	incoming := provider.Provider{ID: existing.ID, Name: existing.Name, Chat: existing.Chat, Key: existing.Key, RefreshEvery: "2h", AutoInclude: []string{"new-*"}}
	got := replaceSharedForTest(t, incoming)
	if got.NoModels {
		t.Error("replacement still selects no models although shared provider exposes its vendor list")
	}
	if got.Routing != "" {
		t.Errorf("routing retained recipient's %q instead of shared smart default", got.Routing)
	}
	if got.PriceRate != 0 {
		t.Errorf("price rate retained recipient's %v instead of shared default", got.PriceRate)
	}
	if len(got.ExtraBody) > 0 {
		t.Error("replacement retains recipient's old extra request body")
	}
	if got.RefreshEvery != incoming.RefreshEvery || !reflect.DeepEqual(got.AutoInclude, incoming.AutoInclude) {
		t.Errorf("explicit imported refresh settings discarded: got %s/%v want %s/%v", got.RefreshEvery, got.AutoInclude, incoming.RefreshEvery, incoming.AutoInclude)
	}
}

func TestSharedProviderDefaultsReplaceRecipientChoices(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := provider.Provider{ID: "share-defaults", Name: "Defaults", Responses: "https://relay.example/v1", Key: "review-key", Off: true,
		BestInFamily: true, Routing: "weight", Affinity: "session", Sink: true, AuthHeader: "bearer", NativeCompaction: true,
		MaxConcurrency: shareLimit(7), MaxAccounts: shareLimit(2), QueueLimit: 20, QueueWait: 60, MaxRPM: 30,
		PriceRate: 0.5, RateWindows: []provider.RateWindow{{Start: "09:00", End: "18:00", Rate: 1.2}},
		ExtraBody: provider.ExtraBody{"temperature": json.RawMessage(`0.1`)}, ReplyHeaders: []string{"X-Trace"}, PassHeaders: []string{"X-Client"},
		Contexts: map[string]int{"model": 10000}, HiddenDrawers: []string{"old-image"}, HiddenVideomakers: []string{"old-video"},
		CodexAsSent: true, AdditionalTools: true, Patience: 30, LimitRestOff: true, QuotaRest: 60, QuotaResetAt: "08:00", RestCap: 60,
		Proxy: "http://127.0.0.1:7890", BalanceToken: "local-balance", Family: "old-family", Website: "https://old.example", KeysURL: "https://old.example/keys"}
	if err := provider.Save(old); err != nil {
		t.Fatal(err)
	}
	incoming := provider.Provider{ID: old.ID, Name: old.Name, Responses: old.Responses, Key: old.Key}
	got := replaceSharedForTest(t, incoming)
	if got.BestInFamily || got.Sink || got.NativeCompaction || got.CodexAsSent || got.AdditionalTools || got.LimitRestOff {
		t.Error("shared defaults retained enabled recipient choices")
	}
	if got.Routing != "" || got.Affinity != "" || got.AuthHeader != "" || got.Family != "" || got.Website != "" || got.KeysURL != "" {
		t.Error("shared defaults retained recipient strings")
	}
	if got.MaxConcurrency != nil || got.MaxAccounts != nil || got.QueueLimit != 0 || got.QueueWait != 0 || got.MaxRPM != 0 || got.PriceRate != 0 || got.Patience != 0 || got.QuotaRest != 0 || got.QuotaResetAt != "" || got.RestCap != 0 {
		t.Error("shared defaults retained recipient limits or pricing")
	}
	if len(got.RateWindows)+len(got.ExtraBody)+len(got.ReplyHeaders)+len(got.PassHeaders)+len(got.Contexts)+len(got.HiddenDrawers)+len(got.HiddenVideomakers) > 0 {
		t.Error("shared defaults retained recipient lists/maps")
	}
	if !got.Off || got.Proxy != old.Proxy || got.BalanceToken != old.BalanceToken {
		t.Error("replacement changed local-only settings")
	}
}

func TestSharedProviderExplicitSettingsSurviveSave(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := provider.Provider{ID: "share-explicit", Name: "Explicit", Responses: "https://relay.example/v1", Key: "review-key", Off: true}
	if err := provider.Save(old); err != nil {
		t.Fatal(err)
	}
	incoming := old
	incoming.BestInFamily, incoming.Sink, incoming.NativeCompaction, incoming.LimitRestOff = true, true, true, true
	incoming.Routing, incoming.Affinity, incoming.AuthHeader = "weight", "session", "bearer"
	incoming.MaxConcurrency, incoming.MaxAccounts = shareLimit(0), shareLimit(3)
	incoming.QueueLimit, incoming.QueueWait, incoming.MaxRPM = 20, 60, 30
	incoming.PriceRate = 0.75
	incoming.RateWindows = []provider.RateWindow{{Start: "09:00", End: "18:00", Rate: 1.2}}
	got := replaceSharedForTest(t, incoming)
	if !got.BestInFamily || !got.Sink || !got.NativeCompaction || !got.LimitRestOff || got.Routing != "weight" || got.Affinity != "session" || got.AuthHeader != "bearer" {
		t.Error("explicit imported choices were lost")
	}
	if got.MaxConcurrency == nil || *got.MaxConcurrency != 0 || got.MaxAccounts == nil || *got.MaxAccounts != 3 || got.QueueLimit != 20 || got.QueueWait != 60 || got.MaxRPM != 30 || got.PriceRate != 0.75 || !reflect.DeepEqual(got.RateWindows, incoming.RateWindows) {
		t.Error("explicit imported limits or pricing were lost")
	}
}

func shareLimit(n int) *int { return &n }

func replaceSharedForTest(t *testing.T, incoming provider.Provider) provider.Provider {
	t.Helper()
	link, err := provider.ShareLink(incoming)
	if err != nil {
		t.Fatal(err)
	}
	id := stashImport(link)
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	importRoutes(mux)
	preview := httptest.NewRecorder()
	mux.ServeHTTP(preview, httptest.NewRequest("GET", "/api/import/"+id, nil))
	var im struct {
		Provider map[string]any `json:"provider"`
		Shared   bool           `json:"shared"`
		Error    string         `json:"error"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &im); err != nil {
		t.Fatal(err)
	}
	if !im.Shared || im.Error != "" {
		t.Fatalf("invalid preview: %s", preview.Body)
	}
	im.Provider["importKeys"] = true
	if _, ok := im.Provider["keys"]; !ok {
		im.Provider["keys"] = []any{}
	}
	body, _ := json.Marshal(im.Provider)
	result := httptest.NewRecorder()
	mux.ServeHTTP(result, httptest.NewRequest("POST", "/api/provider/save", bytes.NewReader(body)))
	if result.Code != 200 {
		t.Fatalf("save: %d %s", result.Code, result.Body)
	}
	stored, err := provider.Stored()
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored: %v %v", stored, err)
	}
	return stored[0]
}
