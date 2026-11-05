package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

// Shared links use a versioned payload rather than a preset and a single key:
// older magpies must refuse them, not silently import only some of the keys.
const sharedLinkLimit = 1 << 20

// ShareLink describes a saved API provider, including every key. It reads no
// accounts, fetches nothing and writes nothing. OAuth and local credentials
// cannot be handed over in an import link.
func ShareLink(p Provider) (string, error) {
	if p.Account != nil || p.IsVertex() || subscriptionID(p.ID) || !hasEndpoint(p) {
		return "", errors.New("only API key providers can be shared as a link")
	}
	if p.IconURL == "" && p.IconFrom != "" {
		p.IconURL, _ = iconURL(p.IconFrom)
	}
	p = portableProvider(p)
	var err error
	if p, err = checkSharedProvider(p); err != nil {
		return "", err
	}
	keys := p.Keys
	if keys == nil {
		keys = []KeyAccount{}
	}
	p.Keys = keys
	p.ImportKeys = true
	b, err := marshalSharedProvider(p)
	if err != nil {
		return "", errors.New("the provider's configuration can't be shared")
	}
	link := Scheme + "://import?format=1&data=" + base64.RawURLEncoding.EncodeToString(b)
	if len(link) > sharedLinkLimit {
		return "", errors.New("the provider's configuration is too large for a share link")
	}
	return link, nil
}

func marshalSharedProvider(p Provider) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	fields["keys"], err = json.Marshal(p.Keys)
	if err != nil {
		return nil, err
	}
	fields["importKeys"], _ = json.Marshal(true)
	return json.Marshal(fields)
}

// portableProvider is the allow-list on both sides of a share. New fields in
// Provider do not become part of links by accident. Local files, proxies,
// account sign-ins, balance credentials and references to other providers stay
// on their computer. IconFrom is a public URL, never the cached file's path.
func portableProvider(p Provider) Provider {
	return WithSharedSettings(Provider{}, p)
}

// WithSharedSettings replaces the portable settings of dst with shared's,
// including empty and default values. Settings local to the recipient stay
// in dst. Export, parsing and a confirmed replacement use this one field list.
func WithSharedSettings(dst, shared Provider) Provider {
	dst.ID, dst.Name, dst.Preset = shared.ID, shared.Name, shared.Preset
	dst.Key, dst.KeyName, dst.KeyProtocol = shared.Key, shared.KeyName, shared.KeyProtocol
	dst.KeyWeight, dst.KeyPlan, dst.Keys = shared.KeyWeight, shared.KeyPlan, shared.Keys
	dst.Chat, dst.Responses, dst.Anthropic, dst.Gemini = shared.Chat, shared.Responses, shared.Anthropic, shared.Gemini
	dst.Decide, dst.BaseAPI, dst.ModelsURL = shared.Decide, shared.BaseAPI, shared.ModelsURL
	dst.Catalog, dst.Website, dst.KeysURL = shared.Catalog, shared.Website, shared.KeysURL
	dst.IconURL, dst.Family = shared.IconURL, shared.Family
	dst.Models, dst.NoModels, dst.BestInFamily = shared.Models, shared.NoModels, shared.BestInFamily
	dst.PickedFrom, dst.Delisted, dst.DelistedAt = shared.PickedFrom, shared.Delisted, shared.DelistedAt
	dst.RefreshEvery, dst.AutoInclude = shared.RefreshEvery, shared.AutoInclude
	dst.Contexts, dst.Unlisted = shared.Contexts, shared.Unlisted
	dst.HiddenDrawers, dst.HiddenVideomakers = shared.HiddenDrawers, shared.HiddenVideomakers
	dst.Headers, dst.ExtraBody = shared.Headers, shared.ExtraBody
	dst.ReplyHeaders, dst.PassHeaders = shared.ReplyHeaders, shared.PassHeaders
	dst.Searches, dst.CodexAsSent = shared.Searches, shared.CodexAsSent
	dst.AdditionalTools, dst.AdditionalToolsOff = shared.AdditionalTools, shared.AdditionalToolsOff
	dst.Patience, dst.PinUpstream = shared.Patience, shared.PinUpstream
	dst.AuthHeader, dst.PassUserAgent, dst.NativeCompaction = shared.AuthHeader, shared.PassUserAgent, shared.NativeCompaction
	dst.Routing, dst.Sink, dst.Affinity = shared.Routing, shared.Sink, shared.Affinity
	dst.MaxConcurrency, dst.MaxAccounts = shared.MaxConcurrency, shared.MaxAccounts
	dst.QueueLimit, dst.QueueWait, dst.MaxRPM = shared.QueueLimit, shared.QueueWait, shared.MaxRPM
	dst.QuotaRest, dst.QuotaResetAt, dst.RestCap = shared.QuotaRest, shared.QuotaResetAt, shared.RestCap
	dst.LimitRestOff, dst.PriceRate, dst.RateWindows, dst.CountFree = shared.LimitRestOff, shared.PriceRate, shared.RateWindows, shared.CountFree
	dst.ImportKeys = shared.ImportKeys
	return dst
}

func sameEndpointHosts(a, b Provider) bool {
	hosts := func(p Provider) map[string]struct{} {
		out := make(map[string]struct{})
		for _, raw := range []string{p.Chat, p.Responses, p.Anthropic, p.Gemini, p.Decide} {
			if host := hostOf(raw); host != "" {
				out[host] = struct{}{}
			}
		}
		return out
	}
	left, right := hosts(a), hosts(b)
	if len(left) != len(right) {
		return false
	}
	for host := range left {
		if _, ok := right[host]; !ok {
			return false
		}
	}
	return true
}

func parseSharedImport(q url.Values) (Provider, error) {
	if q.Get("format") != "1" || len(q["format"]) != 1 {
		return Provider{}, errors.New("this provider link uses an unsupported format; update magpie to import it")
	}
	if len(q["data"]) != 1 || len(q.Get("data")) > sharedLinkLimit {
		return Provider{}, errors.New("the shared provider's configuration is missing or too large")
	}
	b, err := base64.RawURLEncoding.DecodeString(q.Get("data"))
	if err != nil || !utf8.Valid(b) {
		return Provider{}, errors.New("the shared provider's configuration can't be read")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || raw == nil {
		return Provider{}, errors.New("the shared provider's configuration must be an object")
	}
	// ExtraBody's file reader is deliberately lenient; a link must not lose
	// an invalid value silently and add a different configuration instead.
	if body, ok := raw["extraBody"]; ok && string(body) != "null" && !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return Provider{}, errors.New("the shared provider's extra body must be an object")
	}
	var p Provider
	if json.Unmarshal(b, &p) != nil {
		return Provider{}, errors.New("the shared provider's configuration can't be read")
	}
	if p.IsVertex() {
		return Provider{}, errors.New("Google Vertex AI is added with your own Google credentials, not a link")
	}
	if p.Keys == nil {
		p.Keys = []KeyAccount{}
	}
	p, err = checkSharedProvider(portableProvider(p))
	p.ImportKeys = err == nil && p.ImportKeys
	return p, err
}

func checkSharedProvider(p Provider) (Provider, error) {
	p.ID, p.Name = Slug(p.ID), strings.TrimSpace(p.Name)
	if p.ID == "" {
		p.ID = Slug(p.Name)
	}
	if p.ID == "" || p.ID == "magpie" || p.ID == "group" || p.ID == autoID || p.Name == "" {
		return Provider{}, errors.New("the shared provider needs a name and a valid id")
	}
	if len(p.Name) > 80 {
		return Provider{}, errors.New("the shared provider's name is longer than 80 bytes")
	}
	if subscriptionID(p.ID) {
		p.ID = freeID(p.ID)
	}
	if p.Preset != "" {
		pr, err := FromPreset(p.Preset)
		if err != nil || pr.IsVertex() {
			return Provider{}, errors.New("the shared provider names an unsupported preset")
		}
		p.Icon = pr.Icon
	} else if p.Catalog != "" {
		catalogs := p.Catalogs()
		if len(catalogs) == 0 {
			return Provider{}, errors.New("the shared provider's catalog id is empty")
		}
		for _, id := range catalogs {
			if id == "" || id != Slug(id) {
				return Provider{}, errors.New("the shared provider's catalog id is invalid")
			}
		}
		p.Catalog = strings.Join(catalogs, ", ")
		p.Icon = IconForCatalog(catalogs[0])
	}
	for name, dst := range map[string]*string{"chat": &p.Chat, "responses": &p.Responses, "anthropic": &p.Anthropic, "gemini": &p.Gemini, "decide": &p.Decide} {
		value, err := endpoint(name, *dst)
		if err != nil {
			return Provider{}, errorf("the shared provider's %s URL is invalid", name)
		}
		*dst = value
	}
	if p.ModelsURL != "" {
		// Unlike an API base, the exact list URL may have a query (for
		// example OpenRouter's output_modalities filter) and a trailing /.
		u, err := url.Parse(p.ModelsURL)
		if err != nil {
			return Provider{}, errors.New("the shared provider's models URL is invalid")
		}
		base := *u
		base.RawQuery, base.ForceQuery = "", false
		if _, err := endpoint("models", base.String()); err != nil {
			return Provider{}, errors.New("the shared provider's models URL is invalid")
		}
	}
	first := p.first()
	for index, k := range append([]KeyAccount{first}, p.Keys...) {
		if index != 0 && k.Key == "" {
			return Provider{}, errors.New("the shared provider contains an empty additional API key")
		}
		if err := keyProtocolOK(k.Protocol); err != nil {
			return Provider{}, err
		}
		if index != 0 || first.Key != "" { // the empty primary slot is not a key
			if len(k.Key) > 4096 || strings.ContainsFunc(k.Key, func(r rune) bool { return r < ' ' || r == 0x7f }) {
				return Provider{}, errors.New("the shared provider contains an invalid API key")
			}
		}
		if k.Weight < 0 || k.Weight > MaxKeyWeight {
			return Provider{}, errors.New("the shared provider contains an invalid key weight")
		}
	}
	for name, value := range p.Headers {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return Provider{}, errors.New("the shared provider contains an invalid request header")
		}
	}
	for _, why := range []string{ExtraBodyOK(p.ExtraBody), ReplyHeadersOK(p.ReplyHeaders), RequestHeadersOK(p.PassHeaders)} {
		if why != "" {
			return Provider{}, errors.New(why)
		}
	}
	if err := CheckQueue(p.QueueLimit, p.QueueWait); err != nil {
		return Provider{}, err
	}
	if err := CheckRPM(p.MaxRPM); err != nil {
		return Provider{}, err
	}
	if p.MaxConcurrency != nil && (*p.MaxConcurrency < 0 || *p.MaxConcurrency > MaxLimit) {
		return Provider{}, errors.New("the shared provider's concurrency limit is out of range")
	}
	if p.MaxAccounts != nil && (*p.MaxAccounts < 0 || *p.MaxAccounts > MaxLimit) {
		return Provider{}, errors.New("the shared provider's accounts-at-once limit is out of range")
	}
	if why := CheckRateWindows(p.RateWindows); why != "" {
		return Provider{}, errors.New(why)
	}
	if p.QuotaRest < 0 || p.QuotaRest > MaxQuotaRest {
		return Provider{}, errors.New("the shared provider's quota rest is out of range")
	}
	if p.QuotaResetAt != "" {
		if _, err := ParseResetAt(p.QuotaResetAt); err != nil {
			return Provider{}, errors.New("the shared provider's quota reset time is invalid")
		}
	}
	if p.RestCap != RestCapOff && (p.RestCap < 0 || p.RestCap > MaxQuotaRest) {
		return Provider{}, errors.New("the shared provider's rest cap is out of range")
	}
	if why := PriceRateOK(p.PriceRate); why != "" {
		return Provider{}, errors.New(why)
	}
	if !slices.Contains(AuthChoices, p.AuthHeader) {
		return Provider{}, errors.New("the shared provider's key header choice is invalid")
	}
	if !slices.Contains(Affinities, p.Affinity) {
		return Provider{}, errors.New("the shared provider's affinity is invalid")
	}
	if p.Patience < PatienceOff || p.Patience > PatienceLongest {
		return Provider{}, errors.New("the shared provider's patience is out of range")
	}
	p.Website, p.KeysURL = page(p.Website), page(p.KeysURL)
	if p.IconURL != "" {
		if _, err := iconURL(p.IconURL); err != nil {
			return Provider{}, errors.New("the shared provider's icon URL is invalid")
		}
	}
	return p, nil
}
