package gui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderShareAndImportAllKeys(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", h)
	p := provider.Provider{ID: "shared-relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "sk-first", KeyName: "Main", KeyProtocol: provider.Chat, KeyWeight: 3,
		Keys: []provider.KeyAccount{{Name: "Second", Key: "sk-second", Protocol: provider.Chat, Weight: 2}, {Name: "Paused", Key: "sk-third", Off: true}}, Headers: map[string]string{"X-Org": "demo"}, Models: []string{"m1"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(provider.Path())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	importRoutes(mux)
	post := func(action string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/"+action, bytes.NewReader(b)))
		return w
	}
	w := post("share", map[string]string{"id": p.ID})
	if w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	var reply struct {
		Link        string `json:"link"`
		Keys        int    `json:"keys"`
		Credentials bool   `json:"credentials"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Keys != 3 || !reply.Credentials {
		t.Fatalf("shared %+v, want 3 keys and a credential warning", reply)
	}
	got, err := provider.ParseImport(reply.Link)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != p.Key || !reflect.DeepEqual(got.Keys, p.Keys) || got.KeyName != p.KeyName || got.KeyWeight != p.KeyWeight {
		t.Fatalf("shared keys changed: %+v", got)
	}
	after, err := os.ReadFile(provider.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("sharing rewrote providers.json")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response can be cached")
	}
	// Replacing an existing provider uses the link's whole list, including
	// metadata on an unchanged primary key; a plain editor save keeps it.
	got.Keys = []provider.KeyAccount{{Name: "New spare", Key: "sk-new", Off: true}}
	got.KeyName, got.KeyWeight = "Imported main", 5
	got.ImportKeys = true
	body, _ := json.Marshal(got)
	got.ImportKeys = false
	w = post("save", json.RawMessage(body))
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	saved, err := provider.Find(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Keys, got.Keys) || saved.KeyName != got.KeyName || saved.KeyWeight != 5 {
		t.Fatalf("import kept old keys: %+v", saved)
	}
	w = post("save", map[string]any{"id": p.ID, "name": "Edited", "chat": p.Chat})
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	saved, _ = provider.Find(p.ID)
	if !reflect.DeepEqual(saved.Keys, got.Keys) || saved.KeyName != got.KeyName {
		t.Fatal("ordinary save lost imported keys")
	}
	if w := post("share", map[string]string{"id": "missing"}); w.Code == 200 {
		t.Fatal("unknown provider shared")
	}

	// A keyless provider at a local address shares keys: [] and replacing it
	// must not inherit the recipient's old key.
	recipient := provider.Provider{ID: "local", Name: "Local", Chat: "http://127.0.0.1:8080/v1", Key: "old", Keys: []provider.KeyAccount{{Key: "old-spare"}}}
	if err := provider.Save(recipient); err != nil {
		t.Fatal(err)
	}
	sharedLocal := portableProviderForTest(t, recipient)
	sharedLocal.ImportKeys = true
	body, _ = json.Marshal(sharedLocal)
	w = post("save", json.RawMessage(body))
	if w.Code != 200 {
		t.Fatalf("keyless replace: %d %s", w.Code, w.Body.String())
	}
	saved, _ = provider.Find(recipient.ID)
	if saved.Key != "" || len(saved.Keys) != 0 {
		t.Fatalf("keyless replace inherited a key: %+v", saved)
	}
}

func portableProviderForTest(t *testing.T, p provider.Provider) provider.Provider {
	t.Helper()
	recipient := p
	recipient.Key, recipient.Keys = "", []provider.KeyAccount{}
	link, err := provider.ShareLink(recipient)
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.ParseImport(link)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func replyLink(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	var reply struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.Link == "" {
		t.Fatalf("share reply: %v %s", err, w.Body.String())
	}
	return reply.Link
}

func TestProviderImportReplacesAdditionalKeys(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", h)
	p := provider.Provider{ID: "replace", Name: "Replace", Chat: "https://relay.example/v1", Key: "sk-first", Keys: []provider.KeyAccount{{Key: "sk-old"}}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"id": p.ID, "name": p.Name, "chat": p.Chat, "key": p.Key, "keys": []any{}, "importKeys": true})
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/save", bytes.NewReader(b)))
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	saved, _ := provider.Find(p.ID)
	if len(saved.Keys) != 0 {
		t.Fatal("single-key import left the recipient's old keys enabled")
	}
	// The import staging endpoint itself uses the versioned parser.
	link := "magpie://import?format=1&data=" + base64.RawURLEncoding.EncodeToString(b)
	id := stashImport(link)
	importRoutes(mux)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/import/"+id, nil))
	var im importJSON
	_ = json.Unmarshal(w.Body.Bytes(), &im)
	if im.Error != "" || im.Replaces != p.Name || !im.Shared {
		t.Fatalf("import preview: %+v", im)
	}
}
