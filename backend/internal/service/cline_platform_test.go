package service

import (
 "strings"
 "testing"

 "github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)
func clineTestAccount(mode string)*Account{return &Account{ID:42,Platform:PlatformCline,Type:AccountTypeAPIKey,Credentials:map[string]any{"api_key":"test-key-not-real","account_mode":mode,"cline_auth_type":"api_key","model_mapping":map[string]any{"public-model":"cline-pass/model"}}}}
func TestClineAccountDoesNotBecomeDeepseek(t *testing.T){
 a:=clineTestAccount(cline.ModePass)
 if !a.IsCline()||a.IsCNProvider()||a.IsDeepseek(){t.Fatal("Cline platform leaked into CN-provider identity")}
 if !a.IsClineModelSupported("public-model")||a.IsClineModelSupported("other-model"){t.Fatal("explicit Cline whitelist was not enforced")}
 if err:=a.ValidateClineOutboundBody([]byte(`{"model":"cline-pass/model"}`));err!=nil{t.Fatal(err)}
 for _,body:=range []string{`{"model":"vendor/model"}`,`{"model":"cline-pass/other"}`,`{"model":null}`}{if a.ValidateClineOutboundBody([]byte(body))==nil{t.Fatalf("accepted %s",body)}}
 a.Credentials["account_mode"]=cline.ModeFree
 if a.ValidateClineOutboundBody([]byte(`{"model":"cline-pass/model"}`))==nil{t.Fatal("Free public API was enabled")}
}
func TestClineCredentialsAndScopedRotation(t *testing.T){
 a:=clineTestAccount(cline.ModePass)
 if err:=NormalizeClineCredentials(a.Platform,a.Type,a.Credentials);err!=nil{t.Fatal(err)}
 if a.GetClineBaseURL()!=cline.BaseURL||a.GetCredential("api_protocol")!="chat_completions"{t.Fatal("incorrect protocol/default origin")}
 before:=ClineRateLimitScope(a,cline.ScopePass+"weekly");a.Credentials["api_key"]="rotated-test-key"
 if before==ClineRateLimitScope(a,cline.ScopePass+"weekly")||strings.Contains(before,"test-key"){t.Fatal("credential rotation or secret redaction failed")}
 a.Credentials["cline_paid_fallback"]=true;if NormalizeClineCredentials(a.Platform,a.Type,a.Credentials)==nil{t.Fatal("paid fallback accepted")}
 original:=map[string]any{ClineStateExtraKey:"server-state","other":true};incoming:=map[string]any{ClineStateExtraKey:"forged","other":false}
 result:=PreserveClineStateExtra(PlatformCline,original,incoming);if result[ClineStateExtraKey]!="server-state"||incoming[ClineStateExtraKey]!="forged"{t.Fatal("managed state was overwritten or caller input mutated")}
}
