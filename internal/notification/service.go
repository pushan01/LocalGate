package notification

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"localgate/internal/config"
)

const fixedLevel = "active"

var ErrDisabled = errors.New("notifications are not enabled")

type Message struct {
	Title    string
	Subtitle string
	Body     string
	Markdown string
	Group    string
	URL      string
}

type Result struct {
	Status     string
	Provider   string
	HTTPStatus int
	Code       int
	Message    string
}

type Service struct {
	config config.NotificationsConfig
	client *http.Client
}

type barkResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewService(cfg config.NotificationsConfig) (*Service, error) {
	cfg = config.NormalizeNotificationsConfig(cfg)
	if cfg.Enabled {
		if err := validateConfig(cfg); err != nil {
			return nil, err
		}
	}
	return &Service{
		config: cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (s *Service) Enabled() bool {
	return s != nil && s.config.Enabled
}

func (s *Service) SystemEnabled() bool {
	return s.Enabled() && s.config.SystemEnabled
}

func (s *Service) ToolEnabled() bool {
	return s.Enabled() && s.config.ToolEnabled
}

func (s *Service) SendAsync(message Message) {
	if !s.SystemEnabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.Send(ctx, message); err != nil {
			slog.Warn("notification delivery failed", "provider", "bark", "err", err)
		}
	}()
}

func (s *Service) Send(ctx context.Context, message Message) (Result, error) {
	if !s.Enabled() {
		return Result{}, ErrDisabled
	}
	payload, err := messagePayload(message)
	if err != nil {
		return Result{}, err
	}
	requestPayload := make(map[string]any, len(payload)+1)
	if strings.TrimSpace(s.config.Bark.EncryptionKey) != "" {
		ciphertext, err := encryptPayload(payload, []byte(s.config.Bark.EncryptionKey), []byte(s.config.Bark.EncryptionIV))
		if err != nil {
			return Result{}, err
		}
		requestPayload["device_key"] = s.config.Bark.DeviceKey
		requestPayload["ciphertext"] = ciphertext
		requestPayload["iv"] = s.config.Bark.EncryptionIV
	} else {
		for key, value := range payload {
			requestPayload[key] = value
		}
		requestPayload["device_key"] = s.config.Bark.DeviceKey
	}

	body, err := json.Marshal(requestPayload)
	if err != nil {
		return Result{}, err
	}
	endpoint := strings.TrimRight(s.config.Bark.BaseURL, "/") + "/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("bark request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read bark response: %w", err)
	}
	var decoded barkResponse
	_ = json.Unmarshal(responseBody, &decoded)
	result := Result{
		Status:     "accepted",
		Provider:   "bark",
		HTTPStatus: resp.StatusCode,
		Code:       decoded.Code,
		Message:    decoded.Message,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("bark returned HTTP %d: %s", resp.StatusCode, s.redact(responseSummary(responseBody)))
	}
	if decoded.Code != 0 && decoded.Code != http.StatusOK {
		return Result{}, fmt.Errorf("bark rejected notification with code %d: %s", decoded.Code, s.redact(decoded.Message))
	}
	return result, nil
}

func messagePayload(message Message) (map[string]any, error) {
	title := strings.TrimSpace(message.Title)
	body := strings.TrimSpace(message.Body)
	markdown := strings.TrimSpace(message.Markdown)
	if title == "" {
		return nil, errors.New("notification title is required")
	}
	if body == "" && markdown == "" {
		return nil, errors.New("notification body or markdown is required")
	}
	if body != "" && markdown != "" {
		return nil, errors.New("notification body and markdown are mutually exclusive")
	}
	payload := map[string]any{
		"title": title,
		"level": fixedLevel,
	}
	if body != "" {
		payload["body"] = body
	} else {
		payload["markdown"] = markdown
	}
	if value := strings.TrimSpace(message.Subtitle); value != "" {
		payload["subtitle"] = value
	}
	if value := strings.TrimSpace(message.Group); value != "" {
		payload["group"] = value
	}
	if value := strings.TrimSpace(message.URL); value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" {
			return nil, errors.New("notification url must be an absolute URL or URL scheme")
		}
		payload["url"] = value
	}
	return payload, nil
}

func encryptPayload(payload map[string]any, key, iv []byte) (string, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("initialize bark encryption: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("bark encryption IV must be %d bytes", aes.BlockSize)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+padding)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func validateConfig(cfg config.NotificationsConfig) error {
	parsed, err := url.Parse(strings.TrimSpace(cfg.Bark.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("notifications.bark.base_url must be a valid http or https base URL")
	}
	if strings.TrimSpace(cfg.Bark.DeviceKey) == "" {
		return errors.New("notifications.bark.device_key is required")
	}
	key := []byte(cfg.Bark.EncryptionKey)
	iv := []byte(cfg.Bark.EncryptionIV)
	if len(key) == 0 && len(iv) == 0 {
		return nil
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return errors.New("notifications.bark.encryption_key must be 16, 24, or 32 bytes")
	}
	if len(iv) != aes.BlockSize {
		return fmt.Errorf("notifications.bark.encryption_iv must be %d bytes", aes.BlockSize)
	}
	return nil
}

func (s *Service) redact(value string) string {
	for _, secret := range []string{s.config.Bark.DeviceKey, s.config.Bark.EncryptionKey, s.config.Bark.EncryptionIV} {
		if strings.TrimSpace(secret) != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func responseSummary(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300] + "...(truncated)"
	}
	return text
}
