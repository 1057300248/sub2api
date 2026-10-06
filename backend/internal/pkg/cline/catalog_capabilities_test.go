package cline

import (
	"strings"
	"testing"
)

func TestCatalogCapabilitiesUseExactEvidence(t *testing.T) {
	c, err := ParseCatalog([]byte(`{"data":{"clinePass":[{"id":"cline-pass/model"},{"id":"cline-pass/only-paid"}],"recommended":[{"id":"vendor/model"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	err = c.ApplyModelCapabilities([]byte(`{"data":[{"id":"cline-pass/model","contextWindow":131072,"supportsImages":false,"supportsReasoning":true,"supported_parameters":["tools"]},{"id":"vendor/model","context_length":32768,"supportsImages":true},{"id":"vendor/only-paid","supportsReasoning":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cap := c.Pass[0].Capabilities
	if cap == nil || cap.Images == nil || *cap.Images || cap.Reasoning == nil || !*cap.Reasoning || cap.Tools == nil || !*cap.Tools || cap.ContextWindow == nil || *cap.ContextWindow != 131072 {
		t.Fatalf("wrong projection: %+v", cap)
	}
	if c.Pass[1].Capabilities != nil {
		t.Fatal("must not join model suffix across providers")
	}
	if c.Recommended[0].Capabilities.Tools != nil {
		t.Fatal("absent capability is unknown")
	}
	if c.Contains(ModePass, "vendor/only-paid") {
		t.Fatal("catalog enrichment cannot enroll paid models")
	}
}

func TestCatalogCapabilitiesRejectMalformedWithoutPartialUpdate(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"data":[] ,"success":false}`, `[{"id":"x"},{"id":"x"}]`, `[{"id":"ok"},{}]`, strings.Repeat("x", MaxModelCatalogBytes+1)} {
		c := &Catalog{Pass: []Model{{ID: "x"}}}
		if c.ApplyModelCapabilities([]byte(body)) == nil {
			t.Fatal("accepted invalid catalog")
		}
		if c.CapabilitiesStatus != "" || c.Pass[0].Capabilities != nil {
			t.Fatal("partial projection written")
		}
	}
	c := &Catalog{Pass: []Model{{ID: "x"}}}
	if err := c.ApplyModelCapabilities([]byte(`[{"id":"x","supportsImages":"yes","contextWindow":-1,"supportsReasoning":null}]`)); err != nil {
		t.Fatal(err)
	}
	if c.Pass[0].Capabilities != nil {
		t.Fatal("invalid capability values are not evidence")
	}
}
