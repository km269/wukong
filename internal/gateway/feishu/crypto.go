package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/km269/wukong/internal/config"
)

// FeishuCrypto handles signature verification and message decryption
// for Feishu/Lark event callbacks.
//
// Feishu security mechanisms:
//   - SHA256 signature verification (when encrypt_key configured)
//   - AES-256-CBC message body decryption (when encrypt_key configured)
//   - Timestamp freshness check (anti-replay, 5-minute window)
type FeishuCrypto struct {
	encryptKey        string
	verificationToken string
}

// NewFeishuCrypto creates a new FeishuCrypto from channel config.
func NewFeishuCrypto(cfg *config.FeishuChannelConfig) *FeishuCrypto {
	return &FeishuCrypto{
		encryptKey:        cfg.EncryptKey,
		verificationToken: cfg.VerificationToken,
	}
}

// VerifySignature validates the signature in the request headers
// against the computed signature of the body.
//
// Feishu event subscription signature (official spec, see
// https://open.feishu.cn/document/ukTMukTMukTM/uYDNxYjL2QTM24iN0EjN/event-subscription-configure-/encrypt-key-encryption-configuration-case):
//
//	timestamp: Unix timestamp in seconds (X-Lark-Request-Timestamp)
//	nonce:     Random nonce string        (X-Lark-Request-Nonce)
//	encrypt_key: Application Encrypt Key from Feishu app settings
//	body:      Raw request body
//	signature: HEX( SHA256( timestamp + nonce + encrypt_key + body ) )
//
// NOTE: Feishu uses a plain SHA256 over the concatenated string,
// NOT HMAC-SHA256. The key material is the Encrypt Key (encrypt_key),
// NOT the App Secret. The digest is hex-encoded, NOT base64.
//
// The signature is sent in the X-Lark-Signature header.
//
// When no Encrypt Key is configured on the Feishu app, the platform
// does not send signature headers; in that case we skip verification
// (this is the "no encryption" mode). When an Encrypt Key IS
// configured but the signature headers are missing, we reject the
// request as a potential forged callback.
func (fc *FeishuCrypto) VerifySignature(
	headers http.Header,
	body []byte,
) error {
	timestamp := headers.Get("X-Lark-Request-Timestamp")
	nonce := headers.Get("X-Lark-Request-Nonce")
	signature := headers.Get("X-Lark-Signature")

	// No Encrypt Key configured on our side → the platform runs in
	// "no encryption" mode and will not send signature headers.
	// Skip verification (matches Feishu's documented behavior).
	if fc.encryptKey == "" {
		return nil
	}

	// Encrypt Key is configured but the platform did not send
	// signature headers. This is either a forged request or a
	// misconfiguration; reject it rather than silently accepting.
	if timestamp == "" || nonce == "" || signature == "" {
		return fmt.Errorf(
			"feishu: missing signature headers " +
				"(encrypt_key configured but no X-Lark-Signature)")
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

	// Constant-time comparison to prevent timing attacks.
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

// Decrypt decrypts an encrypted Feishu event body.
//
// Feishu encryption scheme:
//  1. The encrypt_key from Feishu app settings is used as the AES
//     key (must be Base64-decoded to get 32 bytes for AES-256).
//  2. The encrypted payload is Base64(AES-256-CBC(plaintext)).
//  3. IV is the first 16 bytes of the decoded key.
//  4. After decryption, strip the random 16-byte prefix from the
//     plaintext.
//  5. PKCS7 padding is removed to get the final JSON.
//
// Returns the decrypted JSON body as bytes.
func (fc *FeishuCrypto) Decrypt(body []byte) ([]byte, error) {
	if fc.encryptKey == "" {
		return nil, fmt.Errorf(
			"feishu: encrypted event received but no encrypt_key configured")
	}

	// Parse the encrypted wrapper.
	var encrypted struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &encrypted); err != nil {
		return nil, fmt.Errorf(
			"feishu: unmarshal encrypted wrapper: %w", err)
	}
	if encrypted.Encrypt == "" {
		return nil, fmt.Errorf(
			"feishu: encrypted field is empty")
	}

	// Decode the AES key from Base64.
	aesKey, err := base64.StdEncoding.DecodeString(fc.encryptKey)
	if err != nil {
		return nil, fmt.Errorf(
			"feishu: decode encrypt_key: %w", err)
	}
	if len(aesKey) != 32 {
		return nil, fmt.Errorf(
			"feishu: invalid AES key length: %d (expected 32)",
			len(aesKey))
	}

	// Decode the encrypted payload from Base64.
	ciphertext, err := base64.StdEncoding.DecodeString(
		encrypted.Encrypt)
	if err != nil {
		return nil, fmt.Errorf(
			"feishu: decode encrypted payload: %w", err)
	}

	// Create AES-256-CBC cipher.
	// IV is the first 16 bytes of the AES key.
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf(
			"feishu: create AES cipher: %w", err)
	}
	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf(
			"feishu: ciphertext too short: %d bytes",
			len(ciphertext))
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf(
			"feishu: ciphertext not block-aligned: %d bytes",
			len(ciphertext))
	}

	iv := aesKey[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)

	// Decrypt in-place.
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding.
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return nil, fmt.Errorf(
			"feishu: remove padding: %w", err)
	}

	// Feishu prepends a random 16-byte string to the plaintext.
	// Strip it to get the actual JSON body.
	if len(plaintext) < 16 {
		return nil, fmt.Errorf(
			"feishu: plaintext too short after decrypt: %d bytes",
			len(plaintext))
	}
	plaintext = plaintext[16:]

	return plaintext, nil
}

// pkcs7Unpad removes PKCS7 padding from the decrypted plaintext.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("pkcs7: empty data")
	}
	paddingLen := int(data[len(data)-1])
	if paddingLen == 0 || paddingLen > aes.BlockSize {
		return nil, fmt.Errorf(
			"pkcs7: invalid padding length: %d", paddingLen)
	}
	if paddingLen > len(data) {
		return nil, fmt.Errorf(
			"pkcs7: padding larger than data: %d > %d",
			paddingLen, len(data))
	}

	// Verify all padding bytes have the correct value.
	for i := range paddingLen {
		if data[len(data)-1-i] != byte(paddingLen) {
			return nil, fmt.Errorf(
				"pkcs7: invalid padding at offset %d", i)
		}
	}

	return data[:len(data)-paddingLen], nil
}

// computeSignature generates the Feishu event signature as a
// lowercase hex string.
//
// Algorithm (per Feishu official spec):
//
//	signature = HEX( SHA256( timestamp + nonce + encrypt_key + body ) )
//
// where "+" denotes plain string/byte concatenation. This is a plain
// SHA256 digest (not HMAC), keyed by the Encrypt Key through
// concatenation rather than as an HMAC key.
func (fc *FeishuCrypto) computeSignature(
	timestamp, nonce string, body []byte,
) string {
	// Build the signature base string:
	// timestamp + nonce + encrypt_key + body
	// The body is included as raw bytes, not JSON-encoded.
	var baseBuilder strings.Builder
	baseBuilder.WriteString(timestamp)
	baseBuilder.WriteString(nonce)
	baseBuilder.WriteString(fc.encryptKey)
	baseBuilder.Write(body)

	sum := sha256.Sum256([]byte(baseBuilder.String()))
	return hex.EncodeToString(sum[:])
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


