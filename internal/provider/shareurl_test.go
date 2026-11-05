package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func sharedTestLink(p any) string {
	if fields, ok := p.(map[string]any); ok {
		fields["importKeys"] = true
	} else if shared, ok := p.(Provider); ok {
		shared.ImportKeys = true
		p = shared
	}
	b, _ := json.Marshal(p)
	return "magpie://import?format=1&data=" + base64.RawURLEncoding.EncodeToString(b)
}

func TestSharedImportKeysAndSettings(t *testing.T) {
	want := Provider{ID: "relay", Name: "中转 & Relay", Key: "sk-first+&=%", KeyName: "Main", KeyProtocol: Responses, KeyWeight: 3,
		Keys: []KeyAccount{{Name: "Messages", Key: "sk-second", Protocol: Anthropic, Weight: 2}, {Name: "Spare", Key: "sk-third", Off: true, Plan: "plan-a"}},
		Chat: "https://relay.example/v1", Responses: "https://relay.example/responses/v1", Anthropic: "https://relay.example/anthropic", Gemini: "https://relay.example/v1beta",
		BaseAPI: "responses", Models: []string{"model,a", "中+文"},
		Headers: map[string]string{"X-Org": "a&b"}, ExtraBody: ExtraBody{"metadata": json.RawMessage(`{"a":"${sessionId}"}`)},
		Routing: "weight", Affinity: "session", AuthHeader: "bearer", CodexAsSent: true, AdditionalTools: true, Patience: 30,
		MaxConcurrency: intPointer(4), QueueLimit: 20, MaxRPM: 60, PriceRate: 0.8,
		RateWindows: []RateWindow{{Start: "09:00", End: "18:00", Rate: 1.5}}}
	p, err := ParseImport(sharedTestLink(want))
	if err != nil {
		t.Fatal(err)
	}
	want.ImportKeys = true
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("import changed configuration: got %+v, want %+v", p, want)
	}
}

func intPointer(value int) *int { return &value }

func TestSharedImportRejectsInvalidPayload(t *testing.T) {
	good := map[string]any{"id": "relay", "name": "Relay", "chat": "https://relay.example/v1", "key": "sk-first"}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"key", "sk\nevil"}, {"keys", []map[string]any{{"key": "sk\nevil"}}},
		{"keys", []map[string]any{{"key": ""}}},
		{"keys", []map[string]any{{"key": "second", "protocol": "unknown"}}},
		{"chat", "https://user:password@relay.example/v1"}, {"chat", "http://relay.example/v1"},
		{"extraBody", []string{"not-an-object"}}, {"extraBody", map[string]any{"messages": "overwritten"}},
		{"headers", map[string]string{"X-Test": "value\r\nInjected: yes"}}, {"preset", VertexPreset},
	} {
		t.Run(field.name+"/"+stringMustJSON(field.value), func(t *testing.T) {
			p := make(map[string]any)
			for k, v := range good {
				p[k] = v
			}
			p[field.name] = field.value
			if _, err := ParseImport(sharedTestLink(p)); err == nil {
				t.Fatal("invalid shared configuration accepted")
			}
		})
	}
	for _, link := range []string{"magpie://import?format=2&data=e30", "magpie://import?format=1&data=%%%", "magpie://import?format=1", "magpie://import?data=e30"} {
		if _, err := ParseImport(link); err == nil {
			t.Fatalf("bad shared link accepted: %s", link)
		}
	}
	// A versioned link from before importKeys still imports its primary key;
	// it does not claim a complete list to clear the user's other keys.
	old := "magpie://import?format=1&data=" + base64.RawURLEncoding.EncodeToString([]byte(`{"id":"relay","name":"Relay","chat":"https://relay.example/v1","key":"sk-one"}`))
	p, err := ParseImport(old)
	if err != nil || p.ImportKeys || p.Key != "sk-one" {
		t.Fatalf("old versioned link: %+v %v", p, err)
	}
}

func stringMustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestSharedImportKeepsPresetOverrides(t *testing.T) {
	p, err := ParseImport(sharedTestLink(Provider{ID: "my-deepseek", Name: "Mine", Preset: "deepseek", Chat: "https://relay.example/custom/v1", Key: "sk-first", KeyPlan: "custom", Keys: []KeyAccount{{Key: "second", Off: true}}}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Preset != "deepseek" || p.Chat != "https://relay.example/custom/v1" || p.KeyPlan != "custom" || len(p.Keys) != 1 || !p.Keys[0].Off {
		t.Fatalf("preset overwrote shared settings: %+v", p)
	}
}

func TestSharedImportKeepsSeveralCatalogs(t *testing.T) {
	p, err := ParseImport(sharedTestLink(Provider{ID: "multi", Name: "Multi", Chat: "https://relay.example/v1", Key: "sk-first", Catalog: "openai  deepseek"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Catalog != "openai, deepseek" || p.Icon != IconForCatalog("openai") {
		t.Fatalf("catalogs changed: %+v", p)
	}
}

func TestSharedImportLeavesLocalStateOut(t *testing.T) {
	p, err := ParseImport(sharedTestLink(Provider{ID: "claude", Name: "Relay", Chat: "https://relay.example/v1", Key: "sk-first", Proxy: "http://proxy-user:password@localhost:7890", BalanceToken: "balance-secret", SecretAccessKey: "cloud-secret", Was: []string{"other"}, Fallback: []string{"mine/model"}, Icon: "file:/private/picture.svg"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "claude" || p.Proxy != "" || p.SecretAccessKey != "" || p.BalanceToken != "" || len(p.Was) > 0 || len(p.Fallback) > 0 || strings.Contains(p.Icon, "file:") {
		t.Fatalf("local state carried into import: %+v", p)
	}
	u, _ := url.Parse(sharedTestLink(p))
	if u.Query().Get("format") != "1" {
		t.Fatal("missing version")
	}
}

func TestShareLinkRefusesSubscriptionAndVertex(t *testing.T) {
	for _, p := range []Provider{
		{ID: "claude", Name: "Claude", Key: "sk-test", Chat: "https://relay.example/v1"},
		{ID: "vertex", Name: "Vertex", Preset: VertexPreset},
	} {
		if link, err := ShareLink(p); err == nil || link != "" {
			t.Fatalf("shared %+v as %q", p, link)
		}
	}
}

func TestShareLinkAlwaysNamesTheCompleteKeyList(t *testing.T) {
	link, err := ShareLink(Provider{ID: "local", Name: "Local", Chat: "http://127.0.0.1:8080/v1", Keys: []KeyAccount{}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	data, err := base64.RawURLEncoding.DecodeString(u.Query().Get("data"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil || string(payload["keys"]) != "[]" || string(payload["importKeys"]) != "true" {
		t.Fatalf("shared payload: %s", data)
	}
}

func TestSharedImportRejectsEmptyCatalog(t *testing.T) {
	for _, catalog := range []string{" ", ",", " , ", "\t,\n"} {
		t.Run(catalog, func(t *testing.T) {
			defer func() {
				if got := recover(); got != nil {
					t.Fatalf("untrusted catalog panics instead of returning a validation error: %v", got)
				}
			}()
			p := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "review-key", Catalog: catalog}
			if _, err := ParseImport(sharedTestLink(p)); err == nil {
				t.Fatal("empty catalog was accepted")
			}
			if _, err := ShareLink(p); err == nil {
				t.Fatal("empty catalog was shared")
			}
		})
	}
}

func TestSharedProviderFetchesItsModelListURL(t *testing.T) {
	isolate(t)
	var inferenceCalls, listCalls atomic.Int32
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inferenceCalls.Add(1)
		http.NotFound(w, r)
	}))
	defer inference.Close()
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/models/" || r.URL.Query().Get("kind") != "chat" || r.Header.Get("Authorization") != "Bearer review-key" {
			t.Errorf("wrong list request: %s, auth=%q", r.URL, r.Header.Get("Authorization"))
			http.Error(w, "wrong list request", 400)
			return
		}
		listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"shared-model"}]}`))
	}))
	defer list.Close()
	source := Provider{ID: "relay", Name: "Relay", Chat: inference.URL + "/v1", ModelsURL: list.URL + "/catalog/models/?kind=chat", Key: "review-key"}
	link, err := ShareLink(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelsURL != source.ModelsURL {
		t.Fatalf("model-list URL lost: got %q, want %q", got.ModelsURL, source.ModelsURL)
	}
	models, err := got.Fetch(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "shared-model" {
		t.Fatalf("shared model discovery: %v, %v", models, err)
	}
	if inferenceCalls.Load() != 0 || listCalls.Load() != 1 {
		t.Fatalf("requests: inference=%d list=%d", inferenceCalls.Load(), listCalls.Load())
	}
}

func TestSharedImportRejectsUnsafeModelListURL(t *testing.T) {
	for _, listURL := range []string{"file:///tmp/models", "https://user@relay.example/models", "http://public.example/models", "https://relay.example/models#fragment", "https://relay.example/%zz"} {
		t.Run(listURL, func(t *testing.T) {
			p := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "review-key", ModelsURL: listURL}
			if _, err := ParseImport(sharedTestLink(p)); err == nil {
				t.Fatal("unsafe model-list URL accepted")
			}
		})
	}
}

func TestSharedProviderKeepsImportedModelSelection(t *testing.T) {
	iconHome(t)
	old := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "review-key", Models: []string{"model-a"}, PickedFrom: []string{"model-a", "old-model"}, Delisted: []string{"old-delisted"}}
	if _, _, err := Restore([]Provider{old}); err != nil {
		t.Fatal(err)
	}
	source := old
	source.PickedFrom = []string{"model-a", "model-b"}
	source.Delisted = []string{"model-c"}
	link, err := ShareLink(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	stored, err := Stored()
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored: %v %v", stored, err)
	}
	if !reflect.DeepEqual(stored[0].PickedFrom, source.PickedFrom) || !reflect.DeepEqual(stored[0].Delisted, source.Delisted) {
		t.Fatalf("imported model selection inherited old state: pickedFrom=%v delisted=%v", stored[0].PickedFrom, stored[0].Delisted)
	}
	if stored[0].ImportKeys {
		t.Fatal("transient import flag was stored")
	}
}

func TestSharedReplacementKeepsRecipientLocalSettings(t *testing.T) {
	iconHome(t)
	existing := Provider{ID: "relay", Name: "Old name", Chat: "https://old.example/v1", Key: "same-key",
		Fallback: []string{"local-fallback/model"}, Unredacted: true, InsecureTLS: true, Off: true,
		Proxy: "http://127.0.0.1:7890", AccountProxies: map[string]string{KeyID("same-key"): "direct"},
		BalanceURL: "https://local.example/balance", BalancePath: "$.credits", BalanceMethod: "POST",
		BalanceToken: "local-balance-token", AccessKeyID: "local-access-id", SecretAccessKey: "local-access-secret"}
	if err := Save(existing); err != nil {
		t.Fatal(err)
	}
	shared := Provider{ID: existing.ID, Name: "Shared name", Chat: "https://shared.example/v1", Key: existing.Key,
		Fallback: []string{"sender-fallback/model"}, Unredacted: false, InsecureTLS: false, Off: false}
	link, err := ShareLink(shared)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(imported); err != nil {
		t.Fatal(err)
	}
	got, err := Find(existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != shared.Name || got.Chat != shared.Chat || !reflect.DeepEqual(got.Fallback, existing.Fallback) {
		t.Fatalf("share or local fallback not applied as intended: %+v", got)
	}
	if !got.Unredacted || got.InsecureTLS || !got.Off {
		t.Error("recipient's local behavior settings were replaced")
	}
	if got.Proxy != existing.Proxy || got.AccountProxies[KeyID("same-key")] != "direct" {
		t.Error("recipient's local proxy settings were replaced")
	}
	if got.BalanceURL != existing.BalanceURL || got.BalancePath != existing.BalancePath || got.BalanceMethod != existing.BalanceMethod || got.BalanceToken != existing.BalanceToken {
		t.Error("recipient's local balance settings were replaced")
	}
	if got.AccessKeyID != existing.AccessKeyID || got.SecretAccessKey != existing.SecretAccessKey {
		t.Error("recipient's local account credentials were replaced")
	}
	if got.ImportKeys {
		t.Error("transient import marker was persisted")
	}
}

func TestSharedReplacementRejectsInsecureTLSForPreset(t *testing.T) {
	iconHome(t)
	old := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "same-key", InsecureTLS: true}
	if err := Save(old); err != nil {
		t.Fatal(err)
	}
	shared := Provider{ID: old.ID, Name: "Relay", Preset: "deepseek", Chat: "https://api.deepseek.com/v1", Key: old.Key}
	link, err := ShareLink(shared)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(imported); err != nil {
		t.Fatalf("replacement: %v", err)
	}
	got, err := Find(old.ID)
	if err != nil || got.Preset != "deepseek" || got.InsecureTLS {
		t.Fatalf("replacement retained an unsafe TLS exception: %+v %v", got, err)
	}
}

func TestSharedReplacementKeepsResolvedIcon(t *testing.T) {
	iconHome(t)
	oldIcon, err := StoreIcon([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="12" height="12"/>`))
	if err != nil {
		t.Fatal(err)
	}
	old := Provider{ID: "relay", Name: "Old", Chat: "https://old.example/v1", Key: "same-key", Icon: oldIcon, IconFrom: "https://old.example/icon.svg"}
	if err := Save(old); err != nil {
		t.Fatal(err)
	}
	newIcon, err := StoreIcon([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="13" height="13"/>`))
	if err != nil {
		t.Fatal(err)
	}
	shared := Provider{ID: old.ID, Name: "Shared", Chat: "https://shared.example/v1", Key: old.Key}
	shared.Icon, shared.IconFrom = oldIcon, "https://cdn.example/shared.svg"
	link, err := ShareLink(shared)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	if imported.IconURL != shared.IconFrom {
		t.Fatalf("share link did not carry the public icon source: %q", imported.IconURL)
	}
	imported.Icon, imported.IconFrom, imported.IconURL = newIcon, imported.IconURL, ""
	if err := Save(imported); err != nil {
		t.Fatal(err)
	}
	got, err := Find(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Icon != newIcon || got.IconFrom != shared.IconFrom {
		t.Fatalf("resolved shared icon was replaced by old local icon: %+v", got)
	}
}
