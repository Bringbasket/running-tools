package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const serviceName = "running-tools"

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Meta struct {
	Service   string  `json:"service"`
	Version   string  `json:"version"`
	RequestID *string `json:"requestId"`
}

type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data"`
	Error *ErrorBody `json:"error"`
	Meta  Meta       `json:"meta"`
}

func WriteData(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, Envelope{
		OK:   true,
		Data: data,
		Meta: responseMeta(r),
	})
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, Envelope{
		OK:   false,
		Data: nil,
		Error: &ErrorBody{
			Code:    code,
			Message: message,
		},
		Meta: responseMeta(r),
	})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("JSON body must contain one object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func responseMeta(r *http.Request) Meta {
	requestID := RequestID(r.Context())
	var pointer *string
	if requestID != "" {
		pointer = &requestID
	}
	return Meta{Service: serviceName, Version: "1", RequestID: pointer}
}
