// Package ruleset resolves reusable named matcher sets for routing rules.
package ruleset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/subscription"
)

const (
	defaultHTTPTimeout = 15 * time.Second
	maxDownloadBytes   = 5 << 20
	cacheVersion       = 1
)

// Cache holds one parsed remote rule-set source.
type Cache struct {
	Version        int      `json:"version"`
	Profile        string   `json:"profile"`
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	Format         string   `json:"format"`
	ETag           string   `json:"etag,omitempty"`
	LastModified   string   `json:"last_modified,omitempty"`
	FetchedTsNs    int64    `json:"fetched_ts_ns"`
	DomainSuffixes []string `json:"domain_suffixes,omitempty"`
	CIDRs          []string `json:"cidrs,omitempty"`
	Skipped        int      `json:"skipped"`
}

// Status reports configured rule-set state and any usable remote cache.
type Status struct {
	Name              string `json:"name"`
	URL               string `json:"url,omitempty"`
	Format            string `json:"format,omitempty"`
	Disabled          bool   `json:"disabled,omitempty"`
	Cached            bool   `json:"cached"`
	FetchedTsNs       int64  `json:"fetched_ts_ns,omitempty"`
	InlineDomainCount int    `json:"inline_domain_count"`
	InlineCIDRCount   int    `json:"inline_cidr_count"`
	DomainCount       int    `json:"domain_count"`
	CIDRCount         int    `json:"cidr_count"`
	Skipped           int    `json:"skipped,omitempty"`
	CacheError        string `json:"cache_error,omitempty"`
	LastError         string `json:"last_error,omitempty"`
}

// StatusPayload is returned by API/mobile surfaces.
type StatusPayload struct {
	Profile  string   `json:"profile"`
	RuleSets []Status `json:"rule_sets"`
}

// RefreshPayload is returned after a manual refresh.
type RefreshPayload struct {
	Profile  string   `json:"profile"`
	RuleSets []Status `json:"rule_sets"`
}

// Resolve builds rule-set matchers from inline entries and usable caches.
func Resolve(configPath string, profile *config.Profile) (map[string]rules.RuleSet, []Status) {
	out := make(map[string]rules.RuleSet)
	if profile == nil {
		return out, nil
	}
	statuses := make([]Status, 0, len(profile.RuleSets))
	for _, cfg := range profile.RuleSets {
		set := rules.RuleSet{
			Domains:        append([]string(nil), cfg.Domains...),
			DomainSuffixes: append([]string(nil), cfg.DomainSuffixes...),
			DomainKeywords: append([]string(nil), cfg.DomainKeywords...),
			CIDRs:          append([]string(nil), cfg.CIDRs...),
		}
		st := baseStatus(cfg)
		if cfg.URL != "" && !cfg.Disabled {
			cache, err := LoadCache(configPath, profile.Name, cfg)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					st.CacheError = err.Error()
				}
			} else {
				set.DomainSuffixes = append(set.DomainSuffixes, cache.DomainSuffixes...)
				set.CIDRs = append(set.CIDRs, cache.CIDRs...)
				fillStatusFromCache(&st, cache)
			}
		}
		out[cfg.Name] = set
		statuses = append(statuses, st)
	}
	return out, statuses
}

// StatusPayloadForProfile returns rule-set status for the selected profile.
func StatusPayloadForProfile(cfg *config.Config, profileName string) (StatusPayload, error) {
	profile, err := selectProfile(cfg, profileName)
	if err != nil {
		return StatusPayload{}, err
	}
	_, statuses := Resolve(cfg.Path, profile)
	return StatusPayload{Profile: profile.Name, RuleSets: statuses}, nil
}

// RefreshProfile fetches selected enabled remote rule sets and updates caches.
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
	statusByName := make(map[string]Status, len(profile.RuleSets))
	for _, set := range profile.RuleSets {
		if len(selected) > 0 {
			if _, ok := selected[set.Name]; !ok {
				continue
			}
		}
		st := baseStatus(set)
		if set.Disabled || strings.TrimSpace(set.URL) == "" {
			statusByName[set.Name] = st
			continue
		}
		err := RefreshOne(ctx, cfg.Path, profile.Name, set, client)
		st = statusForRuleSet(cfg.Path, profile.Name, set)
		if err != nil {
			st.LastError = err.Error()
		}
		statusByName[set.Name] = st
	}
	if len(selected) > 0 {
		for name := range selected {
			if _, ok := statusByName[name]; !ok {
				return RefreshPayload{}, fmt.Errorf("rule set %q not found", name)
			}
		}
	}
	_, statuses := Resolve(cfg.Path, profile)
	for i := range statuses {
		if st, ok := statusByName[statuses[i].Name]; ok {
			statuses[i] = st
		}
	}
	return RefreshPayload{Profile: profile.Name, RuleSets: statuses}, nil
}

// RefreshOne fetches one remote rule set and atomically replaces its cache.
func RefreshOne(ctx context.Context, configPath, profileName string, set config.RuleSetConfig, client *http.Client) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("rule set refresh requires config path")
	}
	if set.Disabled || strings.TrimSpace(set.URL) == "" {
		return nil
	}
	old, oldErr := LoadCache(configPath, profileName, set)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, set.URL, nil)
	if err != nil {
		return err
	}
	if err := subscription.ValidatePublicHTTPURL(ctx, req.URL); err != nil {
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
	resp, err := subscription.ClientWithSafeRedirects(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if oldErr != nil {
			return fmt.Errorf("not modified without existing cache")
		}
		old.FetchedTsNs = time.Now().UnixNano()
		return WriteCache(configPath, profileName, set, old)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("fetch %s: status %d", set.URL, resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxDownloadBytes)
	if err != nil {
		return err
	}
	parsed, err := subscription.Parse(body, set.Format)
	if err != nil {
		return err
	}
	cache := Cache{
		Version:        cacheVersion,
		Profile:        profileName,
		Name:           set.Name,
		URL:            set.URL,
		Format:         parsed.Format,
		ETag:           resp.Header.Get("ETag"),
		LastModified:   resp.Header.Get("Last-Modified"),
		FetchedTsNs:    time.Now().UnixNano(),
		DomainSuffixes: parsed.DomainSuffixes,
		CIDRs:          parsed.CIDRs,
		Skipped:        parsed.Skipped,
	}
	return WriteCache(configPath, profileName, set, cache)
}

// LoadCache reads a rule-set cache.
func LoadCache(configPath, profileName string, set config.RuleSetConfig) (Cache, error) {
	path, err := cachePath(configPath, profileName, set)
	if err != nil {
		return Cache{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, err
	}
	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return Cache{}, fmt.Errorf("parse rule set cache: %w", err)
	}
	if cache.Version != cacheVersion {
		return Cache{}, fmt.Errorf("rule set cache version %d is unsupported", cache.Version)
	}
	if cache.URL != set.URL {
		return Cache{}, fmt.Errorf("rule set cache URL does not match config")
	}
	cache.DomainSuffixes = normalizeDomains(cache.DomainSuffixes)
	cache.CIDRs = normalizeCIDRs(cache.CIDRs)
	return cache, nil
}

// WriteCache atomically writes a rule-set cache.
func WriteCache(configPath, profileName string, set config.RuleSetConfig, cache Cache) error {
	path, err := cachePath(configPath, profileName, set)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create rule set cache dir: %w", err)
	}
	cache.Version = cacheVersion
	cache.Profile = profileName
	cache.Name = set.Name
	cache.URL = set.URL
	cache.DomainSuffixes = normalizeDomains(cache.DomainSuffixes)
	cache.CIDRs = normalizeCIDRs(cache.CIDRs)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rule set cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ruleset-*.json")
	if err != nil {
		return fmt.Errorf("create rule set cache temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod rule set cache temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write rule set cache temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close rule set cache temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace rule set cache: %w", err)
	}
	return nil
}

func baseStatus(set config.RuleSetConfig) Status {
	return Status{
		Name:              set.Name,
		URL:               set.URL,
		Format:            normalizeFormat(set.Format),
		Disabled:          set.Disabled,
		InlineDomainCount: len(set.Domains) + len(set.DomainSuffixes) + len(set.DomainKeywords),
		InlineCIDRCount:   len(set.CIDRs),
		DomainCount:       len(set.Domains) + len(set.DomainSuffixes) + len(set.DomainKeywords),
		CIDRCount:         len(set.CIDRs),
	}
}

func statusForRuleSet(configPath, profileName string, set config.RuleSetConfig) Status {
	st := baseStatus(set)
	cache, err := LoadCache(configPath, profileName, set)
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
	st.DomainCount += len(cache.DomainSuffixes)
	st.CIDRCount += len(cache.CIDRs)
	st.Skipped = cache.Skipped
	if cache.Format != "" {
		st.Format = cache.Format
	}
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(r, limit+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("rule set exceeds %d bytes", limit)
	}
	return body, nil
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

func selectedNames(names []string) (map[string]struct{}, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("rule set name must not be empty")
		}
		out[name] = struct{}{}
	}
	return out, nil
}

func normalizeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return subscription.FormatAuto
	}
	return format
}

func normalizeDomains(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		domain := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, ".")))
		if domain != "" {
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

func cachePath(configPath, profileName string, set config.RuleSetConfig) (string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return "", fmt.Errorf("config path is required")
	}
	dir := filepath.Join(filepath.Dir(configPath), ".clambhook-rule-sets")
	sum := sha256.Sum256([]byte(profileName + "\x00" + set.Name + "\x00" + set.URL))
	hash := hex.EncodeToString(sum[:])[:12]
	name := safeName(profileName) + "-" + safeName(set.Name) + "-" + hash + ".json"
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
