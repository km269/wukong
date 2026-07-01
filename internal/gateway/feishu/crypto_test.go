package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
)

// TestCryptoNewFeishuCrypto verifies that NewFeishuCrypto correctly
// stores all channel configuration fields.
func TestCryptoNewFeishuCrypto(t *testing.T) {
	cfg := makeFeishuConfig("app_123", "secret_xyz",
		"VGhpcyBpcyBhIDMyLWJ5dGUgZW5jcnlwdGlvbjBrZXk", "token_abc")
	crypto := NewFeishuCrypto(cfg)
	if crypto == nil {
		t.Fatal("NewFeishuCrypto returned nil")
	}
	if crypto.appSecret != "secret_xyz" {
		t.Errorf("appSecret = %q, want %q", crypto.appSecret, "secret_xyz")
	}
	if crypto.encryptKey != "VGhpcyBpcyBhIDMyLWJ5dGUgZW5jcnlwdGlvbjBrZXk" {
		t.Errorf("encryptKey mismatch")
	}
	if crypto.verificationToken != "token_abc" {
		t.Errorf("verificationToken = %q, want %q", crypto.verificationToken, "token_abc")
	}
}

// TestCryptoVerifySignatureEmptyHeaders verifies that missing
// signature headers result in no error (passthrough for testing).
func TestCryptoVerifySignatureEmptyHeaders(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "secret", "", ""))
	body := []byte(`{"type":"event_callback"}`)
	headers := http.Header{}

	err := crypto.VerifySignature(headers, body)
	if err != nil {
		t.Errorf("expected nil error for empty headers, got: %v", err)
	}
}

// TestCryptoVerifySignatureValid verifies correct HMAC-SHA256
// signature computation and validation.
func TestCryptoVerifySignatureValid(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "secret", "", ""))
	body := []byte(`{"type":"event_callback","event":{}}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "random_nonce_42"

	// Compute the expected signature manually.
	expected := computeHMACSignature(timestamp, nonce, "secret", body)

	headers := http.Header{}
	headers.Set("X-Lark-Request-Timestamp", timestamp)
	headers.Set("X-Lark-Request-Nonce", nonce)
	headers.Set("X-Lark-Signature", expected)

	err := crypto.VerifySignature(headers, body)
	if err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

// TestCryptoVerifySignatureInvalid verifies that wrong signatures
// are rejected.
func TestCryptoVerifySignatureInvalid(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "secret", "", ""))
	body := []byte(`{"type":"event_callback"}`)

	headers := http.Header{}
	headers.Set("X-Lark-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	headers.Set("X-Lark-Request-Nonce", "random")
	headers.Set("X-Lark-Signature", "invalid_signature_base64=")

	err := crypto.VerifySignature(headers, body)
	if err == nil {
		t.Error("expected error for invalid signature")
	}
}

// TestCryptoIsEncryptedTrue detects an encrypted payload.
func TestCryptoIsEncryptedTrue(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "VGhpcyBpcyBhIDMyLWJ5dGUgZW5jcnlwdGlvbjBrZXk", ""))
	body := []byte(`{"encrypt":"base64blob..."}`)

	if !crypto.IsEncrypted(body) {
		t.Error("expected IsEncrypted to return true for encrypt field")
	}
}

// TestCryptoIsEncryptedFalse detects a plain event payload.
func TestCryptoIsEncryptedFalse(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "VGhpcyBpcyBhIDMyLWJ5dGUgZW5jcnlwdGlvbjBrZXk", ""))
	body := []byte(`{"type":"event_callback"}`)

	if crypto.IsEncrypted(body) {
		t.Error("expected IsEncrypted to return false for plain body")
	}
}

// TestCryptoIsEncryptedNoKey returns false when no encrypt key
// is configured.
func TestCryptoIsEncryptedNoKey(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "", ""))
	body := []byte(`{"encrypt":"base64blob..."}`)

	if crypto.IsEncrypted(body) {
		t.Error("expected IsEncrypted to return false with no encrypt_key")
	}
}

// TestCryptoDecryptRoundTrip encrypts a known plaintext, then
// decrypts it and verifies the result matches.
func TestCryptoDecryptRoundTrip(t *testing.T) {
	// Generate a valid 32-byte AES-256 key.
	aesKey := make([]byte, 32)
	if _, err := rand.Read(aesKey); err != nil {
		t.Fatalf("failed to generate AES key: %v", err)
	}
	encKeyStr := base64.StdEncoding.EncodeToString(aesKey)

	crypto := NewFeishuCrypto(makeFeishuConfig("", "", encKeyStr, ""))

	originalJSON := `{"event":{"type":"im.message.receive_v1",`
	originalJSON += `"msg_type":"text","text":"hello world"}}`

	// Encrypt following the Feishu scheme:
	// 1. Prepend 16 random bytes
	// 2. Apply PKCS7 padding
	// 3. AES-256-CBC encrypt with IV = first 16 bytes of key
	encryptedJSON, err := feishuEncrypt([]byte(originalJSON), aesKey)
	if err != nil {
		t.Fatalf("encrypt for test: %v", err)
	}

	// Wrap in Feishu event format.
	wrapper := map[string]string{
		"encrypt": base64.StdEncoding.EncodeToString(encryptedJSON),
	}
	body, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}

	// Decrypt and verify.
	decrypted, err := crypto.Decrypt(body)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if string(decrypted) != originalJSON {
		t.Errorf("decrypt mismatch:\ngot:  %s\nwant: %s", string(decrypted), originalJSON)
	}
}

// TestCryptoDecryptNoKey returns an error when no encrypt_key
// is configured but an encrypted body is received.
func TestCryptoDecryptNoKey(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "", ""))
	body := []byte(`{"encrypt":"some_encrypted_data"}`)

	_, err := crypto.Decrypt(body)
	if err == nil {
		t.Error("expected error when decrypting without encrypt_key")
	}
}

// TestCryptoDecryptInvalidKey returns an error when the key is
// not a valid Base64 string.
func TestCryptoDecryptInvalidKey(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "!!!not-valid-base64!!!", ""))
	body := []byte(`{"encrypt":"data"}`)

	_, err := crypto.Decrypt(body)
	if err == nil {
		t.Error("expected error for invalid Base64 key")
	}
}

// TestCryptoDecryptTooShort returns an error for invalid
// ciphertext length.
func TestCryptoDecryptTooShort(t *testing.T) {
	aesKey := make([]byte, 32)
	rand.Read(aesKey)
	encKeyStr := base64.StdEncoding.EncodeToString(aesKey)

	crypto := NewFeishuCrypto(makeFeishuConfig("", "", encKeyStr, ""))

	wrapper := map[string]string{
		"encrypt": base64.StdEncoding.EncodeToString([]byte("too_short")),
	}
	body, _ := json.Marshal(wrapper)

	_, err := crypto.Decrypt(body)
	if err == nil {
		t.Error("expected error for short ciphertext")
	}
}

// TestCryptoDecryptEmptyEncryptField returns an error.
func TestCryptoDecryptEmptyEncryptField(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "", "VGhpcyBpcyBhIDMyLWJ5dGUgZW5jcnlwdGlvbjBrZXk", ""))
	body := []byte(`{"encrypt":""}`)

	_, err := crypto.Decrypt(body)
	if err == nil {
		t.Error("expected error for empty encrypt field")
	}
}

// TestComputeSignature verifies the HMAC signature matches the
// Feishu v2 signing spec.
func TestComputeSignature(t *testing.T) {
	crypto := NewFeishuCrypto(makeFeishuConfig("", "test_secret", "", ""))
	body := []byte(`"test_body"`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "abcdef"

	sig := crypto.computeSignature(timestamp, nonce, body)

	// Manually compute the expected signature.
	expected := computeHMACSignature(timestamp, nonce, "test_secret", body)

	if sig != expected {
		t.Errorf("signature = %s, want %s", sig, expected)
	}
}

// TestPKCS7UnpadValid verifies PKCS7 unpadding with valid padding.
func TestPKCS7UnpadValid(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "padding 1",
			input: []byte{0x01, 0x02, 0x03, 0x01},
			want:  "\x01\x02\x03",
		},
		{
			name: "padding 16",
			input: append(make([]byte, 16),
				[]byte{16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16}...),
			want: "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pkcs7Unpad(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", string(got), tt.want)
			}
		})
	}
}

// TestPKCS7UnpadInvalid verifies PKCS7 unpadding rejects
// invalid padding.
func TestPKCS7UnpadInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{name: "empty", input: []byte{}},
		{name: "padding zero", input: []byte{0x01, 0x02, 0x00}},
		{name: "padding too large", input: []byte{0x01, 0x17}},
		{name: "inconsistent padding", input: []byte{0x01, 0x02, 0x03, 0x04}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pkcs7Unpad(tt.input)
			if err == nil {
				t.Error("expected error for invalid padding")
			}
		})
	}
}

// TestParseTimestamp verifies timestamp parsing.
func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"1626778800", 1626778800, false},
		{"0", 0, false},
		{"not_a_number", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseTimestamp(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTimestamp(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseTimestamp(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestAbs verifies the abs helper function.
func TestAbs(t *testing.T) {
	tests := []struct {
		input int64
		want  int64
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-9223372036854775808, -9223372036854775808},
	}

	for _, tt := range tests {
		got := abs(tt.input)
		if got != tt.want {
			t.Errorf("abs(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// makeFeishuConfig creates a FeishuChannelConfig with the given
// fields for test purposes.
func makeFeishuConfig(appID, appSecret, encryptKey, verificationToken string) *config.FeishuChannelConfig {
	return &config.FeishuChannelConfig{
		AppID:             appID,
		AppSecret:         appSecret,
		EncryptKey:        encryptKey,
		VerificationToken: verificationToken,
	}
}

// computeHMACSignature mimics the Feishu v2 signing algorithm for
// test verification purposes.
func computeHMACSignature(timestamp, nonce, secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(nonce))
	mac.Write([]byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuEncrypt encrypts plaintext following the Feishu encryption
// scheme: random 16-byte prefix + PKCS7 padding + AES-256-CBC
// (IV = first 16 bytes of key).
func feishuEncrypt(plaintext []byte, aesKey []byte) ([]byte, error) {
	// 1. Prepend random 16 bytes.
	prefix := make([]byte, 16)
	if _, err := rand.Read(prefix); err != nil {
		return nil, err
	}
	withPrefix := append(prefix, plaintext...)

	// 2. Apply PKCS7 padding.
	padded := pkcs7Pad(withPrefix, aes.BlockSize)

	// 3. AES-256-CBC encrypt.
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}

	iv := aesKey[:aes.BlockSize]
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)

	return ciphertext, nil
}

// pkcs7Pad adds PKCS7 padding to the data.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := make([]byte, padding)
	for i := range padText {
		padText[i] = byte(padding)
	}
	return append(data, padText...)
}
