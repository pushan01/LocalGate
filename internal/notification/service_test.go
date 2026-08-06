package notification

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"localgate/internal/config"
)

func TestSendEncryptedBarkNotification(t *testing.T) {
	const key = "1234567890123456"
	const iv = "1111111111111111"

	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/push" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	service, err := NewService(config.NotificationsConfig{
		Enabled:       true,
		SystemEnabled: true,
		ToolEnabled:   true,
		Bark: config.BarkNotificationConfig{
			BaseURL:       server.URL,
			DeviceKey:     "device-secret",
			EncryptionKey: key,
			EncryptionIV:  iv,
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	result, err := service.Send(context.Background(), Message{
		Title: "Build complete",
		Body:  "Tests passed",
		Group: "localgate-test",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Status != "accepted" || result.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected result: %#v", result)
	}
	if received["device_key"] != "device-secret" {
		t.Fatalf("unexpected device key: %#v", received)
	}
	if _, ok := received["title"]; ok {
		t.Fatalf("encrypted request leaked title: %#v", received)
	}
	ciphertext, _ := received["ciphertext"].(string)
	plain := decryptTestPayload(t, ciphertext, []byte(key), []byte(iv))
	var payload map[string]any
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatalf("decode plaintext: %v", err)
	}
	if payload["title"] != "Build complete" || payload["body"] != "Tests passed" {
		t.Fatalf("unexpected encrypted payload: %#v", payload)
	}
	if payload["level"] != "active" {
		t.Fatalf("expected fixed active level: %#v", payload)
	}
}

func TestMessagePayloadRejectsBodyAndMarkdownTogether(t *testing.T) {
	_, err := messagePayload(Message{Title: "Title", Body: "body", Markdown: "**body**"})
	if err == nil {
		t.Fatal("expected mutually exclusive content error")
	}
}

func decryptTestPayload(t *testing.T, encoded string, key, iv []byte) []byte {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	padding := int(plain[len(plain)-1])
	if padding <= 0 || padding > aes.BlockSize || padding > len(plain) {
		t.Fatalf("invalid padding: %d", padding)
	}
	return plain[:len(plain)-padding]
}
