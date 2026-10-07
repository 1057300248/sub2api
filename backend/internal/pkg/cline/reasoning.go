package cline

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const ReasoningPrefix = "cline:v1:"
const MaxReasoningBytes = 1 << 20
const MaxReasoningBlocks = 128
const ReasoningTTL = 24 * time.Hour

var ErrReasoningContext = errors.New("cline reasoning context is invalid, expired or belongs to a different caller/account/model")

// A gateway-owned, versioned transport envelope, NOT an OpenAI or Anthropic
// native ciphertext. Associated data binds the local caller, upstream identity
// and model. The original provider's opaque JSON is never interpreted as text.
type ReasoningEnvelope struct {
	Details    json.RawMessage `json:"details"`
	Calls      []ReasoningCall `json:"calls,omitempty"`
	TextDigest string          `json:"text_digest"`
	IssuedAt   int64           `json:"issued_at"`
	ExpiresAt  int64           `json:"expires_at"`
}

// ReasoningCall is the exact tool-call material that is needed to bind an
// opaque reasoning continuation to the assistant turn that produced it.
// Keeping the function name and arguments in the envelope prevents a caller
// from reusing valid reasoning details with a different tool invocation that
// happens to reuse the same provider-generated ID.
type ReasoningCall struct {
	ID        string `json:"id"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type ReasoningCodec struct {
	aead    cipher.AEAD
	binding []byte
}

func NewReasoningCodec(secret, binding string) (*ReasoningCodec, error) {
	if len(secret) < 32 || binding == "" {
		return nil, ErrReasoningContext
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("sub2api/cline/reasoning-envelope/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &ReasoningCodec{aead: aead, binding: []byte(binding)}, nil
}
func (c *ReasoningCodec) Seal(details json.RawMessage, calls []ReasoningCall, textDigest string, now time.Time) (string, error) {
	if c == nil {
		return "", ErrReasoningContext
	}
	validated, err := MergeReasoningDetails(nil, details)
	if err != nil {
		return "", err
	}
	if len(validated) == 0 {
		return "", nil
	}
	payload := ReasoningEnvelope{Details: validated, Calls: calls, TextDigest: textDigest, IssuedAt: now.Unix(), ExpiresAt: now.Add(ReasoningTTL).Unix()}
	if !validReasoningEnvelope(&payload) {
		return "", ErrReasoningContext
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if len(raw) > 2*MaxReasoningBytes {
		return "", ErrReasoningContext
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	encrypted := c.aead.Seal(nonce, nonce, raw, c.binding)
	return ReasoningPrefix + base64.RawURLEncoding.EncodeToString(encrypted), nil
}
func (c *ReasoningCodec) Open(token string, now time.Time) (*ReasoningEnvelope, error) {
	if c == nil || !strings.HasPrefix(token, ReasoningPrefix) || len(token) > 3*MaxReasoningBytes {
		return nil, ErrReasoningContext
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, ReasoningPrefix))
	if err != nil || len(raw) < c.aead.NonceSize()+c.aead.Overhead() {
		return nil, ErrReasoningContext
	}
	nonce := raw[:c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, raw[c.aead.NonceSize():], c.binding)
	if err != nil || len(plain) > 2*MaxReasoningBytes {
		return nil, ErrReasoningContext
	}
	var v ReasoningEnvelope
	if json.Unmarshal(plain, &v) != nil || !validReasoningEnvelope(&v) || v.IssuedAt > now.Add(2*time.Minute).Unix() || v.ExpiresAt <= now.Unix() || v.ExpiresAt <= v.IssuedAt || v.ExpiresAt-v.IssuedAt > int64(ReasoningTTL/time.Second) {
		return nil, ErrReasoningContext
	}
	return &v, nil
}
func validReasoningEnvelope(v *ReasoningEnvelope) bool {
	if len(v.Calls) > 128 || len(v.TextDigest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(v.TextDigest); err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, id := range v.Calls {
		if id.ID == "" || len(id.ID) > 256 || seen[id.ID] || strings.ContainsAny(id.ID, "\r\n\x00") {
			return false
		}
		if len(id.Type) > 128 || len(id.Name) > 256 || len(id.Arguments) > MaxReasoningBytes || strings.ContainsAny(id.Type, "\r\n\x00") || strings.ContainsAny(id.Name, "\r\n\x00") {
			return false
		}
		seen[id.ID] = true
	}
	_, err := MergeReasoningDetails(nil, v.Details)
	return err == nil && len(v.Details) > 0
}

// Merge provider deltas by an explicit index or id. Text/data/signature values
// are fragments; stable metadata cannot change halfway through a block. Unknown
// fields are preserved, not silently dropped. Memory and block counts are capped.
func MergeReasoningDetails(previous, delta json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(delta)) == 0 || bytes.Equal(bytes.TrimSpace(delta), []byte("null")) {
		return previous, nil
	}
	if len(previous)+len(delta) > 2*MaxReasoningBytes {
		return nil, ErrReasoningContext
	}
	var current, incoming []map[string]json.RawMessage
	if len(previous) > 0 && json.Unmarshal(previous, &current) != nil {
		return nil, ErrReasoningContext
	}
	if json.Unmarshal(delta, &incoming) != nil || incoming == nil || len(incoming) > MaxReasoningBlocks {
		return nil, ErrReasoningContext
	}
	for _, block := range incoming {
		if len(block) == 0 || len(block) > 32 {
			return nil, ErrReasoningContext
		}
		identity, err := reasoningBlockIdentity(block)
		if err != nil {
			return nil, err
		}
		match := -1
		if identity != "" {
			for i, existing := range current {
				key, e := reasoningBlockIdentity(existing)
				if e != nil {
					return nil, e
				}
				if key == identity {
					match = i
					break
				}
			}
		}
		if match < 0 {
			current = append(current, block)
		} else {
			for key, value := range block {
				old, exists := current[match][key]
				if !exists {
					current[match][key] = value
					continue
				}
				switch key {
				case "text", "data", "signature", "summary":
					var a, b string
					if json.Unmarshal(old, &a) != nil || json.Unmarshal(value, &b) != nil {
						return nil, ErrReasoningContext
					}
					combined, _ := json.Marshal(a + b)
					current[match][key] = combined
				default:
					if !bytes.Equal(bytes.TrimSpace(old), bytes.TrimSpace(value)) {
						return nil, ErrReasoningContext
					}
				}
			}
		}
		if len(current) > MaxReasoningBlocks {
			return nil, ErrReasoningContext
		}
	}
	if len(current) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(current)
	if err != nil || len(raw) > MaxReasoningBytes {
		return nil, ErrReasoningContext
	}
	return raw, nil
}
func reasoningBlockIdentity(v map[string]json.RawMessage) (string, error) {
	if raw, ok := v["index"]; ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 0 || n >= MaxReasoningBlocks {
			return "", ErrReasoningContext
		}
		return "index:" + strconv.Itoa(n), nil
	}
	if raw, ok := v["id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || len(id) > 256 {
			return "", ErrReasoningContext
		}
		if id != "" {
			return "id:" + id, nil
		}
	}
	if raw, ok := v["type"]; ok {
		var name string
		if json.Unmarshal(raw, &name) != nil || len(name) > 128 {
			return "", fmt.Errorf("%w: invalid block type", ErrReasoningContext)
		}
	}
	return "", nil
}
