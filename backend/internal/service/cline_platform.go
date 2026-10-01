package service

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "strings"

 "github.com/Wei-Shaw/sub2api/internal/domain"
 "github.com/Wei-Shaw/sub2api/internal/pkg/cline"
 infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const PlatformCline = domain.PlatformCline
const ClineStateExtraKey = "cline_state"

func (a *Account) IsCline() bool { return a != nil && a.Platform == PlatformCline }
func (a *Account) GetClineMode() string { if !a.IsCline(){return cline.ModeUnknown};return cline.NormalizeMode(a.GetCredential("account_mode")) }
func (a *Account) GetClineBaseURL() string { if !a.IsCline(){return ""};base,err:=cline.NormalizeBaseURL(a.GetCredential("base_url"));if err!=nil{return ""};return base }

// NormalizeClineCredentials is deliberately separate from CN-provider defaults.
// It mutates the caller-owned credentials map after validating the entire input.
func NormalizeClineCredentials(platform,accountType string,credentials map[string]any) error {
 if platform != PlatformCline { return nil }
 bad:=func(message string)error{return infraerrors.BadRequest("INVALID_CLINE_CONFIGURATION",message)}
 if accountType!=AccountTypeAPIKey {return bad("Cline requires an API-key account; account tokens are stored as a distinct credential kind")}
 if credentials==nil {return bad("Cline credentials are required")}
 key,ok:=credentials["api_key"].(string);if !ok||strings.TrimSpace(key)==""{return bad("Cline API key or account token is required")}
 stringField:=func(name string)(string,bool){v,exists:=credentials[name];if !exists{return "",true};s,ok:=v.(string);return s,ok}
 mode,ok:=stringField("account_mode");if !ok{return bad("Cline account_mode must be a string")};if mode==""{mode=cline.ModeUnknown};if mode!=cline.ModeUnknown&&cline.NormalizeMode(mode)==cline.ModeUnknown{return bad("Cline account_mode must be pass, free, payg or unknown")}
 auth,ok:=stringField("cline_auth_type");if !ok{return bad("Invalid Cline credential type")};if auth==""{auth=cline.AuthAPIKey};if auth!=cline.AuthAPIKey&&auth!=cline.AuthAccountToken{return bad("Invalid Cline credential type")}
 rawBase,ok:=stringField("base_url");if !ok{return bad("Invalid Cline base URL")};base,err:=cline.NormalizeBaseURL(rawBase);if err!=nil{return bad(err.Error())}
 protocol,ok:=stringField("api_protocol");if !ok||(protocol!=""&&protocol!="chat_completions"){return bad("Cline upstream uses Chat Completions; native Responses/Messages are not enabled")}
 for _,field:=range []string{"pool_mode","cline_paid_fallback","cline_free_api_enabled","openai_passthrough"}{if v,exists:=credentials[field];exists {enabled,ok:=v.(bool);if !ok||enabled{return bad("Cline does not allow pool retry, passthrough, unverified Free API or paid fallback")}}}
 if raw,exists:=credentials["model_mapping"];exists{
  body,err:=json.Marshal(raw);if err!=nil{return bad("Invalid Cline model mapping")}
  var mapping map[string]string;if json.Unmarshal(body,&mapping)!=nil||mapping==nil{return bad("Cline model mapping must be an explicit object")}
  for publicID,upstreamID:=range mapping{
   if !cline.ValidModelID(publicID)||!cline.ValidModelID(upstreamID){return bad("Cline model mappings must use complete explicit IDs, without wildcards")}
   if mode==cline.ModePass||mode==cline.ModePayG{if err:=cline.ValidateUpstreamModel(mode,upstreamID);err!=nil{return bad(err.Error())}}
  }
 }
 credentials["base_url"]=base;credentials["account_mode"]=mode;credentials["cline_auth_type"]=auth;credentials["api_protocol"]="chat_completions"
 delete(credentials,"api_base_urls")
 return nil
}

func (a *Account) IsClineModelSupported(requestedModel string) bool {
 if !a.IsCline()||a.Type!=AccountTypeAPIKey{return false}
 mapped,exists:=a.GetModelMapping()[requestedModel]
 return exists && cline.ValidModelID(requestedModel) && cline.ValidateUpstreamModel(a.GetClineMode(),mapped)==nil
}

// ValidateClineOutboundBody is the last guard after all alias/reasoning conversions.
func (a *Account) ValidateClineOutboundBody(body []byte) error {
 if !a.IsCline(){return nil}
 if a.Type!=AccountTypeAPIKey{return fmt.Errorf("Cline requires an API-key account")}
 if a.GetClineBaseURL()==""{return fmt.Errorf("invalid Cline base URL")}
 var request struct{Model string `json:"model"`}
 if json.Unmarshal(body,&request)!=nil{return fmt.Errorf("invalid Cline request JSON")}
 if err:=cline.ValidateUpstreamModel(a.GetClineMode(),request.Model);err!=nil{return err}
 for publicID,upstreamID:=range a.GetModelMapping(){if upstreamID==request.Model&&cline.ValidModelID(publicID){return nil}}
 return fmt.Errorf("Cline upstream model is not present in the explicit account model mapping")
}

func ClineCredentialFingerprint(a *Account) string {
 if !a.IsCline(){return ""}
 sum:=sha256.Sum256([]byte(strings.Join([]string{a.GetCredential("api_key"),a.GetClineBaseURL(),a.GetClineMode(),a.GetCredential("cline_auth_type")},"\x00")))
 return hex.EncodeToString(sum[:])
}
func ClineRateLimitScope(a *Account,scope string) string {return "cline:"+ClineCredentialFingerprint(a)+":"+strings.TrimPrefix(scope,"cline:")}

// Cline refresh state is server-owned. An account edit must never manufacture it.
func PreserveClineStateExtra(platform string,existing,incoming map[string]any)map[string]any{
 if platform!=PlatformCline{return incoming}
 out:=make(map[string]any,len(incoming)+1);for k,v:=range incoming{if k!=ClineStateExtraKey{out[k]=v}}
 if state,ok:=existing[ClineStateExtraKey];ok{out[ClineStateExtraKey]=state}
 return out
}
