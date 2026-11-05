package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

func TestProviderShareImportPreview(t *testing.T) {
	t.Run("shows the full shared provider before saving", func(t *testing.T) {
		providerShareHome(t)
		source, link := sharedImportFixture(t)
		parsed, err := provider.ParseImport(link)
		if err != nil {
			t.Fatal(err)
		}
		out, err := printed(t, func() error { return importCmd([]string{"-y", link}) })
		if err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range []string{
			source.Chat,
			source.Responses,
			source.Anthropic,
			source.Gemini,
			source.Decide,
			source.ModelsURL,
		} {
			if !strings.Contains(out, endpoint) {
				t.Errorf("endpoint %s absent from CLI preview:\n%s", endpoint, out)
			}
		}
		for _, key := range []struct {
			name     string
			masked   string
			state    string
			protocol string
		}{
			{source.KeyName, provider.Mask(source.Key), "active", string(source.KeyProtocol)},
			{source.Keys[0].Name, provider.Mask(source.Keys[0].Key), "paused", string(source.Keys[0].Protocol)},
		} {
			line := lineWith(out, key.name)
			for _, want := range []string{key.name, key.masked, key.state, key.protocol} {
				if !strings.Contains(line, want) {
					t.Errorf("%q key line has no %q: %q", key.name, want, line)
				}
			}
		}
		for _, raw := range []string{source.Key, source.Keys[0].Key} {
			if strings.Contains(out, raw) {
				t.Errorf("raw API key %q shown in CLI preview:\n%s", raw, out)
			}
		}
		if parsed.Decide != source.Decide || parsed.ModelsURL != source.ModelsURL {
			t.Errorf("shared link changed endpoints: decide %q, models %q", parsed.Decide, parsed.ModelsURL)
		}
		stored, err := provider.Stored()
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != 1 || len(stored[0].Keys) != 1 || stored[0].Key != source.Key || stored[0].Keys[0] != source.Keys[0] {
			t.Fatalf("shared provider not saved in full: %+v", stored)
		}
	})

	t.Run("keeps control characters out of key names", func(t *testing.T) {
		providerShareHome(t)
		out, err := printed(t, func() error {
			return showImportPreview(provider.Provider{
				ID:      "terminal-relay",
				Name:    "Terminal Relay",
				Chat:    "https://relay.example.test/v1",
				Key:     "review-primary-key-aaaaaaaa",
				KeyName: "Primary\x1b[31m",
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, "Primary [31m") {
			t.Errorf("key name reached the terminal unsanitized:\n%q", out)
		}
	})

	t.Run("saves nothing without confirmation", func(t *testing.T) {
		providerShareHome(t)
		nonterminalStdin(t)
		_, link := sharedImportFixture(t)
		out, err := printed(t, func() error { return importCmd([]string{link}) })
		if err == nil || (!strings.Contains(err.Error(), "not a terminal") && !strings.Contains(err.Error(), "nothing added")) {
			t.Fatalf("unconfirmed import ended with %v:\n%s", err, out)
		}
		if !strings.Contains(out, "key 1") {
			t.Fatalf("unconfirmed import had no preview:\n%s", out)
		}
		stored, readErr := provider.Stored()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(stored) != 0 {
			t.Fatalf("unconfirmed import saved %+v", stored)
		}
	})
	t.Run("keeps recipient-local settings when replacing", func(t *testing.T) {
		providerShareHome(t)
		vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer vendor.Close()
		old := provider.Provider{ID: "local-relay", Name: "Local Relay", Chat: vendor.URL + "/old/v1", Key: "same-key",
			Fallback: []string{"local-fallback/model"}, Off: true, Unredacted: true,
			BalanceURL: vendor.URL + "/balance", BalancePath: "$.credits", BalanceMethod: "POST", BalanceToken: "local-balance"}
		if err := provider.Save(old); err != nil {
			t.Fatal(err)
		}
		shared := provider.Provider{ID: old.ID, Name: "Shared Relay", Chat: vendor.URL + "/new/v1", Key: old.Key,
			Fallback: []string{"sender-fallback/model"}, Off: false, Unredacted: false}
		link, err := provider.ShareLink(shared)
		if err != nil {
			t.Fatal(err)
		}
		out, err := printed(t, func() error { return importCmd([]string{"-y", link}) })
		if err != nil {
			t.Fatalf("import: %v\n%s", err, out)
		}
		got, err := provider.Find(old.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != shared.Name || got.Chat != shared.Chat || !reflect.DeepEqual(got.Fallback, old.Fallback) {
			t.Fatalf("shared fields or local fallback wrong: %+v", got)
		}
		if !got.Off || !got.Unredacted || got.BalanceURL != old.BalanceURL || got.BalancePath != old.BalancePath || got.BalanceMethod != old.BalanceMethod || got.BalanceToken != old.BalanceToken {
			t.Fatalf("recipient-local settings were lost: %+v", got)
		}
	})

}

func providerShareHome(t *testing.T) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	// Separate sibling subtests because appdir.Config reads this on
	// platforms that use the XDG layout for providers.json.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func nonterminalStdin(t *testing.T) {
	t.Helper()
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = old
		_ = stdin.Close()
	})
}

func sharedImportFixture(t *testing.T) (provider.Provider, string) {
	t.Helper()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(vendor.Close)
	source := provider.Provider{
		ID:          "review-relay",
		Name:        "Review Relay",
		Chat:        vendor.URL + "/v1",
		Responses:   vendor.URL + "/responses/v1",
		Anthropic:   vendor.URL + "/anthropic",
		Gemini:      vendor.URL + "/gemini/v1beta",
		Decide:      vendor.URL + "/decide",
		ModelsURL:   vendor.URL + "/models?output_modalities=decisions",
		Key:         "review-primary-key-aaaaaaaa",
		KeyName:     "Review Primary",
		KeyProtocol: provider.Responses,
		Keys: []provider.KeyAccount{{
			Name:     "Review Backup",
			Key:      "review-secondary-key-bbbbbbbb",
			Off:      true,
			Protocol: provider.Anthropic,
		}},
	}
	link, err := provider.ShareLink(source)
	if err != nil {
		t.Fatal(err)
	}
	return source, link
}
