// Package subscription fetches and materializes cached rule blocklists.
package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
)

const (
	FormatAuto    = "auto"
	FormatPlain   = "plain"
	FormatHosts   = "hosts"
	FormatAdblock = "adblock"

	ActionBlock  = "block"
	ActionReject = "reject"

	defaultHTTPTimeout = 15 * time.Second
	maxDownloadBytes   = 5 << 20
	maxEntries         = 200_000
	cacheVersion       = 1
)

// Cache holds one parsed subscription cache file.
type Cache struct {
	Version        int      `json:"version"`
	Profile        string   `json:"profile"`
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	Format         string   `json:"format"`
	Action         string   `json:"action"`
	Networks       []string `json:"networks,omitempty"`
	ETag           string   `json:"etag,omitempty"`
	LastModified   string   `json:"last_modified,omitempty"`
	FetchedTsNs    int64    `json:"fetched_ts_ns"`
	DomainSuffixes []string `json:"domain_suffixes,omitempty"`
	CIDRs          []string `json:"cidrs,omitempty"`
	Skipped        int      `json:"skipped"`
}

// Status reports configured subscription state and any usable cache.
type Status struct {
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	Format         string   `json:"format"`
	Action         string   `json:"action"`
	Networks       []string `json:"networks,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	Cached         bool     `json:"cached"`
	FetchedTsNs    int64    `json:"fetched_ts_ns,omitempty"`
	DomainCount    int      `json:"domain_count"`
	CIDRCount      int      `json:"cidr_count"`
	Skipped        int      `json:"skipped,omitempty"`
	CacheError     string   `json:"cache_error,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
	GeneratedRules []string `json:"generated_rules,omitempty"`
}

// StatusPayload is returned by API/mobile surfaces.
type StatusPayload struct {
	Profile       string   `json:"profile"`
	Subscriptions []Status `json:"subscriptions"`
}

// RefreshPayload is returned after a manual refresh.
type RefreshPayload struct {
	Profile       string   `json:"profile"`
	Subscriptions []Status `json:"subscriptions"`
}

// ParseResult is the normalized result of one subscription body.
type ParseResult struct {
	Format         string
	DomainSuffixes []string
	CIDRs          []string
	Skipped        int
}

// EffectiveRules returns manual, generated, and effective rules for profile.
func EffectiveRules(configPath string, profile *config.Profile) (manual, generated, effective []config.RuleConfig) {
	if profile == nil {
		return nil, nil, nil
	}
	manual = append([]config.RuleConfig(nil), profile.Rules...)
	generated, _ = GeneratedRules(configPath, profile)
	effective = append(append([]config.RuleConfig(nil), manual...), generated...)
	return manual, generated, effective
}

// ProfileWithCachedRules returns a copy of profile whose Rules include cached
// generated subscription rules after manual rules.
func ProfileWithCachedRules(configPath string, profile *config.Profile) config.Profile {
	if profile == nil {
		return config.Profile{}
	}
	out := *profile
	_, _, effective := EffectiveRules(configPath, profile)
	out.Rules = effective
	return out
}

// GeneratedRules materializes all enabled cached subscriptions for profile.
func GeneratedRules(configPath string, profile *config.Profile) ([]config.RuleConfig, []Status) {
	if profile == nil {
		return nil, nil
	}
	statuses := Statuses(configPath, profile)
	rules := make([]config.RuleConfig, 0, len(statuses)*2)
	for _, st := range statuses {
		if st.Disabled || !st.Cached || st.CacheError != "" {
			continue
		}
		sub := findSubscription(profile, st.Name)
		if sub == nil {
			continue
		}
		cache, err := LoadCache(configPath, profile.Name, *sub)
		if err != nil {
			continue
		}
		action := normalizeAction(sub.Action)
		networks := normalizeNetworks(sub.Networks)
		if len(cache.DomainSuffixes) > 0 {
			rules = append(rules, config.RuleConfig{
				Name:           "subscription:" + sub.Name + ":domains",
				Action:         action,
				DomainSuffixes: append([]string(nil), cache.DomainSuffixes...),
				Networks:       networks,
			})
		}
		if len(cache.CIDRs) > 0 {
			rules = append(rules, config.RuleConfig{
				Name:     "subscription:" + sub.Name + ":cidrs",
				Action:   action,
				CIDRs:    append([]string(nil), cache.CIDRs...),
				Networks: networks,
			})
		}
	}
	return rules, statuses
}

// StatusPayloadForProfile returns cache status for the selected profile.
func StatusPayloadForProfile(cfg *config.Config, profileName string) (StatusPayload, error) {
	profile, err := selectProfile(cfg, profileName)
	if err != nil {
		return StatusPayload{}, err
	}
	return StatusPayload{Profile: profile.Name, Subscriptions: Statuses(cfg.Path, profile)}, nil
}

// Statuses returns status rows for each configured subscription.
func Statuses(configPath string, profile *config.Profile) []Status {
	if profile == nil {
		return nil
	}
	rows := make([]Status, 0, len(profile.RuleSubscriptions))
	for _, sub := range profile.RuleSubscriptions {
		st := baseStatus(sub)
		cache, err := LoadCache(configPath, profile.Name, sub)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				st.CacheError = err.Error()
			}
			rows = append(rows, st)
			continue
		}
		fillStatusFromCache(&st, cache)
		rows = append(rows, st)
	}
	return rows
}

// RefreshProfile fetches selected enabled subscriptions and updates their
// caches. Refresh errors are reported per subscription; previous caches remain
// usable.
func RefreshProfile(ctx context.Context, cfg *config.Config, profileName string, names []string, client *http.Client) (RefreshPayload, error) {
	profile, err := selectProfile(cfg, profileName)
	if err != nil {
		return RefreshPayload{}, err
	}
	selected, err := selectedNames(names)
	if err != nil {
		return RefreshPayload{}, err
	}
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	statusByName := make(map[string]Status, len(profile.RuleSubscriptions))
	for _, sub := range profile.RuleSubscriptions {
		if len(selected) > 0 {
			if _, ok := selected[sub.Name]; !ok {
				continue
			}
		}
		if sub.Disabled {
			st := baseStatus(sub)
			st.Disabled = true
			statusByName[sub.Name] = st
			continue
		}
		err := RefreshOne(ctx, cfg.Path, profile.Name, sub, client)
		st := statusForSubscription(cfg.Path, profile.Name, sub)
		if err != nil {
			st.LastError = err.Error()
		}
		statusByName[sub.Name] = st
	}
	if len(selected) > 0 {
		for name := range selected {
			if _, ok := statusByName[name]; !ok {
				return RefreshPayload{}, fmt.Errorf("rule subscription %q not found", name)
			}
		}
	}
	statuses := Statuses(cfg.Path, profile)
	for i := range statuses {
		if st, ok := statusByName[statuses[i].Name]; ok {
			statuses[i] = st
		}
	}
	return RefreshPayload{Profile: profile.Name, Subscriptions: statuses}, nil
}

// RefreshOne fetches one subscription and atomically replaces its cache.
func RefreshOne(ctx context.Context, configPath, profileName string, sub config.RuleSubscriptionConfig, client *http.Client) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("subscription refresh requires config path")
	}
	if sub.Disabled {
		return nil
	}
	old, oldErr := LoadCache(configPath, profileName, sub)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
	if err != nil {
		return err
	}
	if err := ValidatePublicHTTPURL(ctx, req.URL); err != nil {
		return err
	}
	if oldErr == nil {
		if old.ETag != "" {
			req.Header.Set("If-None-Match", old.ETag)
		}
		if old.LastModified != "" {
			req.Header.Set("If-Modified-Since", old.LastModified)
		}
	}
	resp, err := ClientWithSafeRedirects(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if oldErr != nil {
			return fmt.Errorf("not modified without existing cache")
		}
		old.FetchedTsNs = time.Now().UnixNano()
		return WriteCache(configPath, profileName, sub, old)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("fetch %s: status %d", sub.URL, resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxDownloadBytes)
	if err != nil {
		return err
	}
	parsed, err := Parse(body, sub.Format)
	if err != nil {
		return err
	}
	cache := Cache{
		Version:        cacheVersion,
		Profile:        profileName,
		Name:           sub.Name,
		URL:            sub.URL,
		Format:         parsed.Format,
		Action:         normalizeAction(sub.Action),
		Networks:       normalizeNetworks(sub.Networks),
		ETag:           resp.Header.Get("ETag"),
		LastModified:   resp.Header.Get("Last-Modified"),
		FetchedTsNs:    time.Now().UnixNano(),
		DomainSuffixes: parsed.DomainSuffixes,
		CIDRs:          parsed.CIDRs,
		Skipped:        parsed.Skipped,
	}
	return WriteCache(configPath, profileName, sub, cache)
}

// Parse normalizes a subscription body.
func Parse(body []byte, format string) (ParseResult, error) {
	format = normalizeFormat(format)
	if format == FormatAuto {
		format = detectFormat(body)
	}
	seenDomains := make(map[string]struct{})
	seenCIDRs := make(map[string]struct{})
	var skipped int
	lines := bytes.Split(body, []byte{'\n'})
	for _, rawLine := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(string(rawLine), "\ufeff"))
		if line == "" {
			continue
		}
		var domains []string
		var cidrs []string
		var ok bool
		switch format {
		case FormatAdblock:
			domains, cidrs, ok = parseAdblockLine(line)
		case FormatHosts:
			domains, cidrs, ok = parseHostsLine(line)
		default:
			domains, cidrs, ok = parsePlainLine(line)
		}
		if !ok {
			skipped++
			continue
		}
		for _, domain := range domains {
			if _, exists := seenDomains[domain]; !exists {
				if len(seenDomains)+len(seenCIDRs) >= maxEntries {
					return ParseResult{}, fmt.Errorf("subscription has more than %d entries", maxEntries)
				}
				seenDomains[domain] = struct{}{}
			}
		}
		for _, cidr := range cidrs {
			if _, exists := seenCIDRs[cidr]; !exists {
				if len(seenDomains)+len(seenCIDRs) >= maxEntries {
					return ParseResult{}, fmt.Errorf("subscription has more than %d entries", maxEntries)
				}
				seenCIDRs[cidr] = struct{}{}
			}
		}
	}
	if len(seenDomains)+len(seenCIDRs) == 0 {
		return ParseResult{}, fmt.Errorf("subscription produced no usable entries")
	}
	out := ParseResult{
		Format:         format,
		DomainSuffixes: sortedKeys(seenDomains),
		CIDRs:          sortedKeys(seenCIDRs),
		Skipped:        skipped,
	}
	return out, nil
}

// LoadCache reads a subscription cache.
func LoadCache(configPath, profileName string, sub config.RuleSubscriptionConfig) (Cache, error) {
	path, err := cachePath(configPath, profileName, sub)
	if err != nil {
		return Cache{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, err
	}
	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return Cache{}, fmt.Errorf("parse cache: %w", err)
	}
	if cache.Version != cacheVersion {
		return Cache{}, fmt.Errorf("cache version %d is unsupported", cache.Version)
	}
	if cache.URL != sub.URL {
		return Cache{}, fmt.Errorf("cache URL does not match subscription")
	}
	cache.DomainSuffixes = normalizeDomains(cache.DomainSuffixes)
	cache.CIDRs = normalizeCIDRs(cache.CIDRs)
	return cache, nil
}

// WriteCache atomically writes a subscription cache.
func WriteCache(configPath, profileName string, sub config.RuleSubscriptionConfig, cache Cache) error {
	path, err := cachePath(configPath, profileName, sub)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create subscription cache dir: %w", err)
	}
	cache.Version = cacheVersion
	cache.Profile = profileName
	cache.Name = sub.Name
	cache.URL = sub.URL
	cache.Action = normalizeAction(cache.Action)
	cache.Networks = normalizeNetworks(cache.Networks)
	cache.DomainSuffixes = normalizeDomains(cache.DomainSuffixes)
	cache.CIDRs = normalizeCIDRs(cache.CIDRs)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".subscription-*.json")
	if err != nil {
		return fmt.Errorf("create subscription cache temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod subscription cache temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write subscription cache temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close subscription cache temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace subscription cache: %w", err)
	}
	return nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("subscription body exceeds %d bytes", limit)
	}
	return data, nil
}

func detectFormat(body []byte) string {
	lines := bytes.Split(body, []byte{'\n'})
	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "||") || strings.Contains(line, "##") || strings.HasPrefix(line, "@@") {
			return FormatAdblock
		}
		fields := strings.Fields(line)
		if len(fields) > 1 {
			if _, err := netip.ParseAddr(fields[0]); err == nil {
				return FormatHosts
			}
		}
	}
	return FormatPlain
}

func parsePlainLine(line string) ([]string, []string, bool) {
	line = stripInlineComment(line)
	if line == "" {
		return nil, nil, true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, nil, true
	}
	var domains []string
	var cidrs []string
	for _, field := range fields {
		d, c, ok := parseToken(field)
		if !ok {
			continue
		}
		if d != "" {
			domains = append(domains, d)
		}
		if c != "" {
			cidrs = append(cidrs, c)
		}
	}
	return domains, cidrs, len(domains)+len(cidrs) > 0
}

func parseHostsLine(line string) ([]string, []string, bool) {
	line = stripInlineComment(line)
	if line == "" {
		return nil, nil, true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, nil, true
	}
	start := 0
	if _, err := netip.ParseAddr(fields[0]); err == nil && len(fields) > 1 {
		start = 1
	}
	var domains []string
	var cidrs []string
	for _, field := range fields[start:] {
		d, c, ok := parseToken(field)
		if !ok {
			continue
		}
		if d != "" {
			domains = append(domains, d)
		}
		if c != "" {
			cidrs = append(cidrs, c)
		}
	}
	return domains, cidrs, len(domains)+len(cidrs) > 0
}

func parseAdblockLine(line string) ([]string, []string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "@@") {
		return nil, nil, true
	}
	if strings.Contains(line, "##") || strings.Contains(line, "#@#") || strings.Contains(line, "#$#") {
		return nil, nil, true
	}
	if strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/") {
		return nil, nil, false
	}
	if i := strings.IndexByte(line, '$'); i >= 0 {
		line = line[:i]
	}
	if strings.HasPrefix(line, "||") {
		host := line[2:]
		host = trimAdblockHost(host)
		if domain := normalizeDomain(host); domain != "" {
			return []string{domain}, nil, true
		}
		return nil, nil, false
	}
	if strings.HasPrefix(line, "|http://") || strings.HasPrefix(line, "|https://") {
		line = strings.TrimPrefix(line, "|")
		if u, err := url.Parse(line); err == nil {
			if domain := normalizeDomain(u.Hostname()); domain != "" {
				return []string{domain}, nil, true
			}
		}
		return nil, nil, false
	}
	return parsePlainLine(line)
}

func trimAdblockHost(host string) string {
	cut := len(host)
	for _, sep := range []string{"^", "/", ":", "?", "&", "|"} {
		if i := strings.Index(host, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	return host[:cut]
}

func stripInlineComment(line string) string {
	if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return ""
	}
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

func parseToken(token string) (domain, cidr string, ok bool) {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, `"'`)
	token = strings.TrimPrefix(token, "||")
	token = strings.TrimPrefix(token, "|")
	token = strings.TrimPrefix(token, "*.")
	token = strings.TrimSuffix(token, "^")
	token = strings.TrimSuffix(token, ".")
	if token == "" {
		return "", "", false
	}
	if prefix, err := netip.ParsePrefix(token); err == nil {
		return "", prefix.Masked().String(), true
	}
	if addr, err := netip.ParseAddr(token); err == nil {
		bits := 128
		if addr.Is4() {
			bits = 32
		}
		return "", netip.PrefixFrom(addr, bits).String(), true
	}
	if strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") {
		u, err := url.Parse(token)
		if err != nil {
			return "", "", false
		}
		token = u.Hostname()
	}
	if domain := normalizeDomain(token); domain != "" {
		return domain, "", true
	}
	return "", "", false
}

func normalizeDomain(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.Trim(raw, "[]")
	raw = strings.TrimPrefix(raw, "*.")
	raw = strings.TrimSuffix(raw, ".")
	if raw == "" || strings.ContainsAny(raw, " \t\r\n/*?&=|^") {
		return ""
	}
	if strings.HasPrefix(raw, ".") || strings.Contains(raw, "..") {
		return ""
	}
	if _, err := netip.ParseAddr(raw); err == nil {
		return ""
	}
	labels := strings.Split(raw, ".")
	if len(labels) < 2 {
		return ""
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return ""
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return ""
		}
	}
	return raw
}

func normalizeDomains(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		if domain := normalizeDomain(raw); domain != "" {
			seen[domain] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func normalizeCIDRs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			seen[prefix.Masked().String()] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func baseStatus(sub config.RuleSubscriptionConfig) Status {
	return Status{
		Name:     sub.Name,
		URL:      sub.URL,
		Format:   normalizeFormat(sub.Format),
		Action:   normalizeAction(sub.Action),
		Networks: normalizeNetworks(sub.Networks),
		Disabled: sub.Disabled,
	}
}

func statusForSubscription(configPath, profileName string, sub config.RuleSubscriptionConfig) Status {
	st := baseStatus(sub)
	cache, err := LoadCache(configPath, profileName, sub)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			st.CacheError = err.Error()
		}
		return st
	}
	fillStatusFromCache(&st, cache)
	return st
}

func fillStatusFromCache(st *Status, cache Cache) {
	st.Cached = true
	st.FetchedTsNs = cache.FetchedTsNs
	st.DomainCount = len(cache.DomainSuffixes)
	st.CIDRCount = len(cache.CIDRs)
	st.Skipped = cache.Skipped
	if len(cache.DomainSuffixes) > 0 {
		st.GeneratedRules = append(st.GeneratedRules, "subscription:"+cache.Name+":domains")
	}
	if len(cache.CIDRs) > 0 {
		st.GeneratedRules = append(st.GeneratedRules, "subscription:"+cache.Name+":cidrs")
	}
	if cache.Format != "" {
		st.Format = cache.Format
	}
}

func selectedNames(names []string) (map[string]struct{}, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("subscription name must not be empty")
		}
		out[name] = struct{}{}
	}
	return out, nil
}

func selectProfile(cfg *config.Config, profileName string) (*config.Profile, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return cfg.ActiveProfile()
	}
	profile, ok := cfg.ProfileByName(profileName)
	if !ok {
		return nil, fmt.Errorf("profile %q not found", profileName)
	}
	return profile, nil
}

func findSubscription(profile *config.Profile, name string) *config.RuleSubscriptionConfig {
	if profile == nil {
		return nil
	}
	for i := range profile.RuleSubscriptions {
		if profile.RuleSubscriptions[i].Name == name {
			return &profile.RuleSubscriptions[i]
		}
	}
	return nil
}

func normalizeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return FormatAuto
	}
	return format
}

func normalizeAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return ActionBlock
	}
	return action
}

func normalizeNetworks(networks []string) []string {
	out := make([]string, 0, len(networks))
	seen := make(map[string]struct{}, len(networks))
	for _, raw := range networks {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func cachePath(configPath, profileName string, sub config.RuleSubscriptionConfig) (string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return "", fmt.Errorf("config path is required")
	}
	dir := filepath.Join(filepath.Dir(configPath), ".clambhook-subscriptions")
	sum := sha256.Sum256([]byte(profileName + "\x00" + sub.Name + "\x00" + sub.URL))
	hash := hex.EncodeToString(sum[:])[:12]
	name := safeName(profileName) + "-" + safeName(sub.Name) + "-" + hash + ".json"
	return filepath.Join(dir, name), nil
}

func safeName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}
