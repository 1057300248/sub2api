package cline

import (
	"bytes"
	"encoding/json"
	"io"
)

// prepareJSONCompletion accepts either a standard completion or exactly one
// explicit success/data wrapper. Raw data bytes are retained, including unknown
// usage/tool/reasoning fields; decoding and re-encoding numbers can lose precision.
func prepareJSONCompletion(payload []byte) ([]byte, []byte, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil || root == nil {
		return nil, nil, ErrInvalidJSONBody
	}
	if HasGenerationError(payload) {
		return nil, payload, ErrStreamFailure
	}
	if success, wrapped := root["success"]; wrapped {
		flag := bytes.TrimSpace(success)
		if bytes.Equal(flag, []byte("false")) {
			return nil, nil, ErrStreamFailure
		}
		if !bytes.Equal(flag, []byte("true")) {
			return nil, nil, ErrInvalidJSONBody
		}
		// Ambiguous outer choices/usage must not compete with the nested response.
		for _, key := range []string{"choices", "usage", "model"} {
			if _, ok := root[key]; ok {
				return nil, nil, ErrInvalidJSONBody
			}
		}
		data := root["data"]
		var inner map[string]json.RawMessage
		if json.Unmarshal(data, &inner) != nil || inner == nil {
			return nil, nil, ErrInvalidJSONBody
		}
		if HasGenerationError(data) {
			return nil, data, ErrStreamFailure
		}
		if _, recursive := inner["success"]; recursive {
			return nil, nil, ErrInvalidJSONBody
		}
		payload = data
	}
	return payload, nil, nil
}

// Validate the shape consumed by all three bridges before exposing any bytes.
// Missing usage stays missing; no token count, price or successful text is invented.
func validateJSONCompletion(payload []byte) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil || root == nil {
		return ErrInvalidJSONBody
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil || len(choices) == 0 {
		return ErrInvalidJSONBody
	}
	for _, choice := range choices {
		if choice == nil {
			return ErrInvalidJSONBody
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(choice["message"], &message) != nil || message == nil {
			return ErrInvalidJSONBody
		}
	}
	if raw, exists := root["usage"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var usage map[string]json.RawMessage
		if json.Unmarshal(raw, &usage) != nil || usage == nil {
			return ErrInvalidJSONBody
		}
	}
	return nil
}

func (b *guardedJSONBody) preparePayload(payload []byte) ([]byte, error) {
	normalized, failure, err := prepareJSONCompletion(payload)
	if err != nil {
		if len(failure) > 0 && b.onError != nil {
			b.onError(failure)
		}
		return nil, err
	}
	// A JSON success never masquerades as an empty successful streaming response.
	if b.expectSSE {
		return nil, ErrExpectedSSE
	}
	if err := validateJSONCompletion(normalized); err != nil {
		return nil, err
	}
	return normalized, io.EOF
}
