package toolargs

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDecodeMap(t *testing.T) {
	got, err := Decode(map[string]any{"path": "/tmp/a"}, false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["path"] != "/tmp/a" {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestDecodeBase64String(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"path": "/tmp/a"})
	encoded := base64.StdEncoding.EncodeToString(src)
	got, err := Decode(encoded, true)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["path"] != "/tmp/a" {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestDecodeBase64EnvelopeObject(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"path": "/tmp/a"})
	encoded := base64.StdEncoding.EncodeToString(src)
	got, err := Decode(map[string]any{"base64data": encoded}, true)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["path"] != "/tmp/a" {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestDecodeRejectsStringWhenDisabled(t *testing.T) {
	_, err := Decode("eyJwYXRoIjoiL3RtcC9hIn0=", false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeHTTPBodyAcceptsRawBase64(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"path": "/tmp/a"})
	encoded := base64.StdEncoding.EncodeToString(src)
	got, err := DecodeHTTPBody([]byte(encoded), true)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["path"] != "/tmp/a" {
		t.Fatalf("unexpected args: %#v", got)
	}
}
