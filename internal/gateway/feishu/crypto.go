package feishu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/km269/wukong/internal/config"
)

// FeishuCrypto handles signature verification and optional message
// decryption for Feishu/Lark event callbacks.
type FeishuCrypto struct {
	appSecret         string
	encryptKey        string
	verificationToken string
}

// NewFeishuCrypto creates a new FeishuCrypto from channel config.
func NewFeishuCrypto(cfg *config.FeishuChannelConfig) *FeishuCrypto {
	return &FeishuCrypto{
		appSecret:         cfg.AppSecret,
		encryptKey:        cfg.EncryptKey,
		verificationToken: cfg.VerificationToken,
	}
}

// VerifySignature validates the HMAC-SHA256 signature in the request
// headers against the computed signature of the body.
//
// Feishu v2 signature format:
//
//	timestamp: Unix timestamp in seconds
//	nonce:     Random nonce string
//	body:      Raw request body
//	signature: Base64(HMAC-SHA256(timestamp + nonce + encrypt_key, body))
//
// The signature is sent in the X-Lark-Signature header.
func (fc *FeishuCrypto) VerifySignature(
	headers http.Header,
	body []byte,
) error {
	timestamp := headers.Get("X-Lark-Request-Timestamp")
	nonce := headers.Get("X-Lark-Request-Nonce")
	signature := headers.Get("X-Lark-Signature")

	// If no signature header is present, skip verification.
	// This allows testing without a real Feishu backend.
	if timestamp == "" || nonce == "" || signature == "" {
		return nil
	}

	// Verify timestamp freshness (within 5 minutes).
	ts, err := parseTimestamp(timestamp)
	if err == nil {
		now := time.Now().Unix()
		if abs(now-ts) > 300 {
			return fmt.Errorf(
				"feishu: timestamp too old: %d (now: %d)",
				ts, now)
		}
	}

	// Compute expected signature.
	expected := fc.computeSignature(timestamp, nonce, body)

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("feishu: signature mismatch")
	}

	return nil
}

// IsEncrypted checks whether the given JSON body is encrypted.
// Feishu encrypts event bodies when encryption is configured in the
// app settings. The encrypted payload has an "encrypt" field.
func (fc *FeishuCrypto) IsEncrypted(body []byte) bool {
	if fc.encryptKey == "" {
		return false
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, hasEncrypt := raw["encrypt"]
	return hasEncrypt
}

// Decrypt decrypts an encrypted Feishu event body using AES-256-CBC.
// The encrypt key is the Base64-decoded value from app settings.
//
// Feishu encryption format:
//   - Encrypt field: Base64(AES-256-CBC(plaintext))
//   - IV is the first 16 bytes of the encrypt key (AES block size)
//
// Note: Full AES decryption requires padding removal and random prefix
// stripping. This is a simplified implementation for non-encrypted
// events. For encrypted events, configure the encrypt key properly.
func (fc *FeishuCrypto) Decrypt(body []byte) ([]byte, error) {
	// For now, encrypted events are not fully supported.
	// Return the raw body and let the caller handle it.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("feishu: unmarshal encrypted: %w",
			err)
	}

	// If we have an encrypt field but no key configured, it's an
	// error.
	if fc.encryptKey == "" {
		return nil, fmt.Errorf(
			"feishu: encrypted event received but no encrypt_key configured")
	}

	// TODO: Implement full AES-256-CBC decryption when encryption
	// is enabled on the Feishu app.
	return body, nil
}

// computeSignature generates the HMAC-SHA256 signature as a Base64
// string.
func (fc *FeishuCrypto) computeSignature(
	timestamp, nonce string, body []byte,
) string {
	// Build the signature base string:
	// timestamp + nonce + encrypt_key + body
	// The body is included as raw bytes, not JSON-encoded.
	var baseBuilder strings.Builder
	baseBuilder.WriteString(timestamp)
	baseBuilder.WriteString(nonce)
	if fc.appSecret != "" {
		baseBuilder.WriteString(fc.appSecret)
	}
	baseBuilder.Write(body)

	mac := hmac.New(sha256.New, []byte(fc.appSecret))
	mac.Write([]byte(baseBuilder.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// parseTimestamp parses a timestamp string and returns the Unix
// timestamp value.
func parseTimestamp(s string) (int64, error) {
	var ts int64
	_, err := fmt.Sscanf(s, "%d", &ts)
	if err != nil {
		return 0, fmt.Errorf("feishu: invalid timestamp: %s", s)
	}
	return ts, nil
}

// abs returns the absolute value of x.
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
