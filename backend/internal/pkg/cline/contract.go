// Package cline owns Cline policy without depending on gateway or database types.
package cline

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	Platform         = "cline"
	BaseURL          = "https://api.cline.bot/api/v1"
	CatalogURL       = BaseURL + "/ai/cline/recommended-models"
	UsageURL         = BaseURL + "/users/me/plan/usage-limits"
	ModePass         = "pass"
	ModeFree         = "free"
	ModePayG         = "payg"
	ModeUnknown      = "unknown"
	AuthAPIKey       = "api_key"
	AuthAccountToken = "account_token"
	MaxBodyBytes     = 256 << 10
)

var ErrFreeAPIUnsupported = errors.New("Cline Free is IDE/CLI-only; unverified public API forwarding is disabled")

func NormalizeMode(value string) string {
	switch value {
	case ModePass, ModeFree, ModePayG:
		return value
	default:
		return ModeUnknown
	}
}

func NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return BaseURL, nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("Cline base URL must be HTTPS without credentials, query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.EqualFold(u.Hostname(), "api.cline.bot") {
		if u.Port() != "" && u.Port() != "443" {
			return "", errors.New("Cline official origin only supports port 443")
		}
		switch u.Path {
		case "", "/api", "/api/v1":
			return BaseURL, nil
		default:
			return "", errors.New("use the Cline API base, not a request endpoint")
		}
	}
	if strings.HasSuffix(u.Path, "/chat/completions") || strings.HasSuffix(u.Path, "/responses") || strings.HasSuffix(u.Path, "/messages") {
		return "", errors.New("use a base URL, not an inference endpoint")
	}
	return u.String(), nil
}
func IsOfficialBase(value string) bool {
	base, err := NormalizeBaseURL(value)
	return err == nil && base == BaseURL
}
func ValidModelID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, "*\\\x00") && strings.IndexFunc(id, unicode.IsSpace) == -1
}
func ValidateUpstreamModel(mode, id string) error {
	if !ValidModelID(id) {
		return errors.New("Cline requires an explicit non-wildcard upstream model ID")
	}
	switch mode {
	case ModeFree:
		return ErrFreeAPIUnsupported
	case ModePass:
		if !strings.HasPrefix(id, "cline-pass/") || len(id) == len("cline-pass/") {
			return errors.New("ClinePass requires the complete cline-pass/ model ID; paid fallback is disabled")
		}
	case ModePayG:
		if strings.HasPrefix(id, "cline-pass/") {
			return errors.New("PAYG cannot use a ClinePass model")
		}
	default:
		return errors.New("confirm the Cline usage mode before forwarding")
	}
	return nil
}

type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}
type Catalog struct {
	Recommended []Model `json:"recommended"`
	Pass        []Model `json:"clinePass"`
	Free        []Model `json:"free"`
}

func ParseCatalog(body []byte) (*Catalog, error) {
	if len(body) > MaxBodyBytes {
		return nil, errors.New("Cline catalog is too large")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, errors.New("invalid Cline catalog")
	}
	c := &Catalog{}
	known := false
	for key, target := range map[string]*[]Model{"recommended": &c.Recommended, "clinePass": &c.Pass, "free": &c.Free} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		known = true
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, target) != nil {
			return nil, fmt.Errorf("invalid Cline catalog bucket: %s", key)
		}
		seen := map[string]bool{}
		for _, model := range *target {
			if !ValidModelID(model.ID) || seen[model.ID] {
				return nil, errors.New("invalid or duplicate Cline model ID")
			}
			seen[model.ID] = true
		}
	}
	if !known {
		return nil, errors.New("unrecognized Cline catalog schema")
	}
	return c, nil
}
func (c *Catalog) Models(mode string) []Model {
	if c == nil {
		return nil
	}
	switch mode {
	case ModePass:
		return c.Pass
	case ModeFree:
		return c.Free
	case ModePayG:
		return c.Recommended
	default:
		return nil
	}
}
func (c *Catalog) Contains(mode, id string) bool {
	for _, m := range c.Models(mode) {
		if m.ID == id {
			return true
		}
	}
	return false
}

type Window struct {
	Type        string     `json:"type"`
	PercentUsed float64    `json:"percent_used"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// Missing windows remain unknown. This optional metadata endpoint is not a stable public contract.
func ParseUsage(body []byte) ([]Window, error) {
	if len(body) > MaxBodyBytes {
		return nil, errors.New("Cline usage response is too large")
	}
	var p struct {
		Success *bool `json:"success"`
		Data    *struct {
			Limits *[]struct {
				Type        string   `json:"type"`
				PercentUsed *float64 `json:"percentUsed"`
				ResetsAt    *string  `json:"resetsAt"`
			} `json:"limits"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &p) != nil || p.Success == nil || !*p.Success || p.Data == nil || p.Data.Limits == nil {
		return nil, errors.New("unrecognized Cline usage response")
	}
	out := make([]Window, 0)
	seen := map[string]bool{}
	for _, raw := range *p.Data.Limits {
		switch raw.Type {
		case "five_hour", "weekly", "monthly":
		default:
			continue
		}
		if seen[raw.Type] || raw.PercentUsed == nil || math.IsNaN(*raw.PercentUsed) || math.IsInf(*raw.PercentUsed, 0) || *raw.PercentUsed < 0 || *raw.PercentUsed > 100 {
			return nil, errors.New("invalid Cline quota percentage or duplicate window")
		}
		seen[raw.Type] = true
		w := Window{Type: raw.Type, PercentUsed: *raw.PercentUsed}
		if raw.ResetsAt != nil {
			at, err := time.Parse(time.RFC3339, *raw.ResetsAt)
			if err != nil {
				return nil, errors.New("invalid Cline quota reset timestamp")
			}
			w.ResetsAt = &at
		}
		out = append(out, w)
	}
	return out, nil
}
