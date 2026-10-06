package cline

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

// This is an observation, not a provider-selection policy. It never retries,
// changes models or substitutes the requested provider for missing evidence.
type ProviderObservation struct {
	Requested        []string  `json:"requested,omitempty"`
	ConstraintStatus string    `json:"constraint_status"`
	Actual           string    `json:"actual,omitempty"`
	Status           string    `json:"status"`
	Source           string    `json:"source,omitempty"`
	UpstreamModel    string    `json:"upstream_model"`
	ObservedAt       time.Time `json:"observed_at"`
	Complete         bool      `json:"complete"`
	HTTPStatus       int       `json:"http_status"`
}

func validProviderName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:/", c) {
			continue
		}
		return false
	}
	return true
}
func RequestedProviderObservation(body []byte) ProviderObservation {
	out := ProviderObservation{ConstraintStatus: "unspecified", Status: "unknown"}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return out
	}
	_ = json.Unmarshal(root["model"], &out.UpstreamModel)
	raw, exists := nestedProviderRaw(root, "providerOptions", "gateway", "only")
	if !exists {
		return out
	}
	out.ConstraintStatus = "invalid"
	var only []string
	if json.Unmarshal(raw, &only) != nil || len(only) == 0 || len(only) > 16 {
		return out
	}
	for _, name := range only {
		if !validProviderName(name) {
			return out
		}
	}
	out.Requested = only
	out.ConstraintStatus = "specified"
	return out
}
func nestedProviderRaw(root map[string]json.RawMessage, path ...string) (json.RawMessage, bool) {
	for i, key := range path {
		raw, ok := root[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return raw, true
		}
		var next map[string]json.RawMessage
		if json.Unmarshal(raw, &next) != nil || next == nil {
			return nil, false
		}
		root = next
	}
	return nil, false
}
func (o *ProviderObservation) ObservePacket(packet []byte) {
	if len(packet) > 1<<20 {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(packet, &root) != nil || root == nil {
		return
	}
	for _, path := range [][]string{{"provider"}, {"provider_metadata", "gateway", "routing", "finalProvider"}, {"providerMetadata", "gateway", "routing", "finalProvider"}} {
		raw, ok := nestedProviderRaw(root, path...)
		if !ok {
			continue
		}
		var actual string
		if json.Unmarshal(raw, &actual) != nil {
			continue
		}
		actual = strings.ToLower(strings.TrimSpace(actual))
		if !validProviderName(actual) {
			continue
		}
		if o.Status == "conflicting" {
			continue
		}
		if o.Actual != "" && !strings.EqualFold(o.Actual, actual) {
			o.Actual = ""
			o.Status = "conflicting"
			o.Source = "conflicting_reports"
			continue
		}
		o.Actual = actual
		o.Source = strings.Join(path, ".")
		o.Status = "observed"
		if o.ConstraintStatus == "specified" {
			o.Status = "mismatch"
			for _, wanted := range o.Requested {
				if strings.EqualFold(wanted, actual) {
					o.Status = "matched"
					break
				}
			}
		}
	}
}
func ValidProviderObservation(o *ProviderObservation) bool {
	if o == nil || !ValidModelID(o.UpstreamModel) || o.ObservedAt.IsZero() || len(o.Requested) > 16 {
		return false
	}
	for _, p := range o.Requested {
		if !validProviderName(p) {
			return false
		}
	}
	if o.Actual != "" && !validProviderName(o.Actual) {
		return false
	}
	switch o.Status {
	case "unknown", "conflicting", "observed", "matched", "mismatch":
	default:
		return false
	}
	switch o.ConstraintStatus {
	case "unspecified", "specified", "invalid":
	default:
		return false
	}
	switch o.Source {
	case "", "provider", "provider_metadata.gateway.routing.finalProvider", "providerMetadata.gateway.routing.finalProvider", "conflicting_reports":
	default:
		return false
	}
	return o.HTTPStatus >= 100 && o.HTTPStatus <= 599
}

// Observes the validated response without altering any bytes or framing. The
// wrapper delegates completion to the Cline SSE guard so lifetime/usage handling
// cannot accidentally lose its validated terminal signal. Extra memory is 1MiB.
func ObserveProviderBody(source io.ReadCloser, stream bool, observation ProviderObservation, done func(ProviderObservation)) io.ReadCloser {
	return &providerObservationBody{source: source, stream: stream, observation: observation, done: done}
}

type providerObservationBody struct {
	source      io.ReadCloser
	stream      bool
	observation ProviderObservation
	done        func(ProviderObservation)
	mu          sync.Mutex
	buffer      []byte
	discard     bool
	complete    bool
	closed      bool
	once        sync.Once
	closeErr    error
}

func (b *providerObservationBody) ClineSSEComplete() bool {
	signal, ok := b.source.(interface{ ClineSSEComplete() bool })
	return ok && signal.ClineSSEComplete()
}
func (b *providerObservationBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	b.mu.Lock()
	if !b.closed {
		if b.stream {
			for _, ch := range p[:n] {
				if ch == '\n' {
					if !b.discard {
						line := bytes.TrimSpace(b.buffer)
						if bytes.HasPrefix(line, []byte("data:")) {
							packet := bytes.TrimSpace(line[5:])
							if bytes.Equal(packet, []byte("[DONE]")) {
								b.complete = true
							} else {
								b.observation.ObservePacket(packet)
							}
						}
					}
					b.buffer = b.buffer[:0]
					b.discard = false
				} else if !b.discard {
					if len(b.buffer) >= 1<<20 {
						b.buffer = nil
						b.discard = true
					} else {
						b.buffer = append(b.buffer, ch)
					}
				}
			}
			if signal, ok := b.source.(interface{ ClineSSEComplete() bool }); ok {
				b.complete = signal.ClineSSEComplete()
			}
		} else {
			if !b.discard {
				if len(b.buffer)+n > 1<<20 {
					b.buffer = nil
					b.discard = true
				} else {
					b.buffer = append(b.buffer, p[:n]...)
				}
			}
			if err == io.EOF {
				b.complete = true
				if !b.discard {
					b.observation.ObservePacket(b.buffer)
				}
			}
		}
		if err != nil && err != io.EOF {
			b.complete = false
		}
	}
	b.mu.Unlock()
	return n, err
}
func (b *providerObservationBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.source.Close()
		b.mu.Lock()
		b.closed = true
		b.observation.Complete = b.complete && b.observation.HTTPStatus >= 200 && b.observation.HTTPStatus < 300
		b.observation.ObservedAt = time.Now().UTC()
		snapshot := b.observation
		b.buffer = nil
		b.mu.Unlock()
		if b.done != nil {
			b.done(snapshot)
		}
	})
	return b.closeErr
}
