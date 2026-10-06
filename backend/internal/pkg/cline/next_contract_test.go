//go:build unit

package cline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestClineWireCredentials(t *testing.T) {
	for _, tt := range []struct {
		raw, kind, want string
		valid           bool
	}{
		{"raw.jwt.token", AuthAccountToken, "workos:raw.jwt.token", true},
		{"workos:raw.jwt.token", AuthAccountToken, "workos:raw.jwt.token", true},
		{" WORKOS:raw.jwt.token ", AuthAccountToken, "workos:raw.jwt.token", true},
		{"api-key", AuthAPIKey, "api-key", true},
		{"workos:as-api-key", AuthAPIKey, "workos:as-api-key", true},
		{"", AuthAccountToken, "", false}, {"workos:", AuthAccountToken, "", false},
		{"workos:workos:abc", AuthAccountToken, "", false}, {"a\r\nb", AuthAPIKey, "", false},
		{"a b", AuthAccountToken, "", false}, {"abc", "oauth", "", false},
	} {
		t.Run(tt.kind+"/"+tt.raw, func(t *testing.T) {
			got, err := WireCredential(tt.raw, tt.kind)
			if (err == nil) != tt.valid || got != tt.want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestClineReasoningEnvelopeBindingAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	details := json.RawMessage(`[{"type":"reasoning.encrypted","id":"r1","data":"opaque","signature":"sig"}]`)
	h := sha256.Sum256([]byte("answer"))
	digest := hex.EncodeToString(h[:])
	codec, err := NewReasoningCodec(strings.Repeat("x", 32), "tenant/key/subject/model")
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Seal(details, []string{"call-1"}, digest, now)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := codec.Open(token, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := MergeReasoningDetails(nil, details)
	if !bytes.Equal(opened.Details, canonical) || len(opened.Calls) != 1 || opened.Calls[0] != "call-1" {
		t.Fatalf("lost payload: %+v", opened)
	}
	other, _ := NewReasoningCodec(strings.Repeat("x", 32), "other/key/subject/model")
	rotated, _ := NewReasoningCodec(strings.Repeat("y", 32), "tenant/key/subject/model")
	for _, bad := range []*ReasoningCodec{other, rotated} {
		if _, e := bad.Open(token, now); e == nil {
			t.Fatal("unbound replay allowed")
		}
	}
	if _, e := codec.Open(token, now.Add(25*time.Hour)); e == nil {
		t.Fatal("expired replay allowed")
	}
	if _, e := codec.Open(token, now.Add(-3*time.Minute)); e == nil {
		t.Fatal("future replay allowed")
	}
	raw := []byte(token)
	raw[len(raw)/2] ^= 1
	if _, e := codec.Open(string(raw), now); e == nil {
		t.Fatal("tamper accepted")
	}
	if _, e := codec.Open("foreign-native-ciphertext", now); e == nil {
		t.Fatal("foreign ciphertext accepted")
	}
	if _, e := codec.Seal(details, []string{"same", "same"}, digest, now); e == nil {
		t.Fatal("duplicate calls accepted")
	}
	if _, e := NewReasoningCodec("weak", "tenant"); e == nil {
		t.Fatal("weak key accepted")
	}
}
func TestClineReasoningFragmentsAndBounds(t *testing.T) {
	first := json.RawMessage(`[{"index":0,"type":"reasoning.encrypted","data":"a","signature":"s"}]`)
	second := json.RawMessage(`[{"index":0,"data":"b","signature":"t"}]`)
	merged, e := MergeReasoningDetails(first, second)
	if e != nil {
		t.Fatal(e)
	}
	var rows []map[string]any
	if json.Unmarshal(merged, &rows) != nil {
		t.Fatal(string(merged))
	}
	if len(rows) != 1 || rows[0]["data"] != "ab" || rows[0]["signature"] != "st" {
		t.Fatal(string(merged))
	}
	for _, bad := range []string{`{}`, `[null]`, `[{"index":-1}]`, `[{"index":128}]`, `[{"index":0,"type":"changed"}]`} {
		if _, e = MergeReasoningDetails(first, []byte(bad)); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	huge, _ := json.Marshal([]map[string]any{{"data": strings.Repeat("a", MaxReasoningBytes)}})
	if _, e = MergeReasoningDetails(nil, huge); e == nil {
		t.Fatal("unbounded reasoning accepted")
	}
	if v, e := MergeReasoningDetails(nil, []byte("null")); e != nil || len(v) != 0 {
		t.Fatalf("null: %s %v", v, e)
	}
}
func TestClineCatalogCapabilitiesAreExplicitAndCloudIsNotEnabled(t *testing.T) {
	c, e := ParseCatalog([]byte(`{"data":{"clinePass":[{"id":"cline-pass/m"},{"id":"cline-pass/unknown"}],"free":[],"clineCloud":[{"id":"cloud/private"}]}}`))
	if e != nil {
		t.Fatal(e)
	}
	e = EnrichCatalogCapabilities(c, []byte(`{"data":[{"id":"cline-pass/m","context_length":131072,"top_provider":{"max_completion_tokens":8192},"supportsImages":false,"supported_parameters":["tools","reasoning"],"reasoningEfforts":["low","high","impossible"]},{"id":"cline-pass/unknown","name":"vision reasoning cached"},{"id":"cloud/private","supportsImages":true}]}`))
	if e != nil {
		t.Fatal(e)
	}
	cap := c.Pass[0].Capabilities
	if cap == nil || *cap.ContextWindow != 131072 || *cap.MaxOutputTokens != 8192 || cap.SupportsImages == nil || *cap.SupportsImages || !*cap.SupportsTools || !*cap.SupportsReasoning || len(cap.ReasoningEfforts) != 2 {
		t.Fatalf("bad capabilities: %+v", cap)
	}
	if c.Pass[1].Capabilities != nil || c.Contains(ModePass, "cloud/private") {
		t.Fatal("invented capability/permission")
	}
	raw, _ := json.Marshal(cap)
	if !strings.Contains(string(raw), `"supports_images":false`) {
		t.Fatal("false omitted")
	}
	if e = EnrichCatalogCapabilities(c, []byte(`[{"id":"vendor/m","supportsImages":true}]`)); e != nil || c.Pass[0].Capabilities != nil {
		t.Fatal("stripped prefix to infer support")
	}
	if e = EnrichCatalogCapabilities(c, []byte(`[{"id":"m"},{"id":"m"}]`)); e == nil {
		t.Fatal("ambiguous catalog accepted")
	}
	if _, e = ParseCatalog([]byte(`{"clineCloud":[]}`)); e == nil {
		t.Fatal("cloud enabled")
	}
}
func TestClineProviderObservationNeverInventsActual(t *testing.T) {
	req := []byte(`{"model":"cline-pass/m","providerOptions":{"gateway":{"only":["deepseek"]}}}`)
	o := RequestedProviderObservation(req)
	o.ObservePacket([]byte(`{"choices":[{"message":{"content":"provider is deepseek"}}]}`))
	if o.Actual != "" || o.Status != "unknown" {
		t.Fatal("content became evidence")
	}
	o.ObservePacket([]byte(`{"provider_metadata":{"gateway":{"routing":{"finalProvider":"DeepSeek"}}}}`))
	if o.Actual != "deepseek" || o.Status != "matched" {
		t.Fatalf("%+v", o)
	}
	o.ObservePacket([]byte(`{"provider":"other"}`))
	if o.Actual != "" || o.Status != "conflicting" {
		t.Fatal("conflict hidden")
	}
	o.ObservePacket([]byte(`{"provider":"deepseek"}`))
	if o.Status != "conflicting" {
		t.Fatal("conflict erased")
	}
	m := RequestedProviderObservation(req)
	m.ObservePacket([]byte(`{"provider":"other"}`))
	if m.Status != "mismatch" {
		t.Fatal("mismatch hidden")
	}
	fake := RequestedProviderObservation([]byte(`{"model":"m","only":["deepseek"],"providerOptions":{"gateway":{}}}`))
	if fake.ConstraintStatus != "unspecified" {
		t.Fatal("outer fields leaked into nested lookup")
	}
}
func TestClineProviderObservationBodyPreservesWireAndCompletion(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "sse", false: "json"}[stream], func(t *testing.T) {
			body := `{"provider":"deepseek","choices":[]}`
			if stream {
				body = "data: " + body + "\n\ndata: [DONE]\n\n"
			}
			called := 0
			var result ProviderObservation
			o := RequestedProviderObservation([]byte(`{"model":"m"}`))
			o.HTTPStatus = 200
			reader := ObserveProviderBody(io.NopCloser(strings.NewReader(body)), stream, o, func(v ProviderObservation) { called++; result = v })
			raw, e := io.ReadAll(reader)
			if e != nil {
				t.Fatal(e)
			}
			if string(raw) != body {
				t.Fatal("wire changed")
			}
			if e = reader.Close(); e != nil {
				t.Fatal(e)
			}
			_ = reader.Close()
			if called != 1 || !result.Complete || result.Actual != "deepseek" || !ValidProviderObservation(&result) {
				t.Fatalf("%+v calls%d", result, called)
			}
		})
	}
	called := false
	o := ProviderObservation{HTTPStatus: 200}
	r := ObserveProviderBody(io.NopCloser(strings.NewReader("data: {}\n\n")), true, o, func(v ProviderObservation) {
		called = true
		if v.Complete {
			t.Error("truncated completed")
		}
	})
	_, _ = io.ReadAll(r)
	_ = r.Close()
	if !called {
		t.Fatal("callback missing")
	}
}
