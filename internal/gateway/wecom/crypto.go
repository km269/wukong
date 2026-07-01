package wecom

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/km269/wukong/internal/config"
)

// WeComCrypto handles signature verification, AES encryption/
// decryption, and URL callback validation for WeCom enterprise
// applications and AI bots.
//
// WeCom encryption scheme (for enterprise app callbacks):
//  1. SHA1(Token, Timestamp, Nonce, Encrypt) → msg_signature
//  2. AES-256-CBC(EncodingAESKey) decrypt Encrypt → plaintext
//  3. plaintext = random16 + msg_len4 + msg + corpid
type WeComCrypto struct {
	token          string
	corpID         string
	encodingAESKey []byte // 32-byte AES key decoded from Base64
}

// NewWeComCrypto creates a WeComCrypto from channel configuration.
// encodingAESKey is the 43-character Base64 string from WeCom admin.
func NewWeComCrypto(cfg *config.WeComChannelConfig) *WeComCrypto {
	wc := &WeComCrypto{
		token:  cfg.Token,
		corpID: cfg.CorpID,
	}

	// Decode the Base64 AES key (43 chars + "=" → 32 bytes).
	if cfg.EncodingAESKey != "" {
		key := cfg.EncodingAESKey + "="
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err == nil && len(decoded) == 32 {
			wc.encodingAESKey = decoded
		}
	}

	return wc
}

// HasCryptoEnabled returns true when encryption is configured.
func (wc *WeComCrypto) HasCryptoEnabled() bool {
	return len(wc.encodingAESKey) == 32 && wc.token != ""
}

// VerifyURLSignature validates the SHA1 msg_signature for URL
// verification requests (GET with echostr).
//
// WeCom signature algorithm:
//
//	Sorted(Token, Timestamp, Nonce, Encrypt) → join → SHA1
func (wc *WeComCrypto) VerifyURLSignature(
	msgSignature, timestamp, nonce, encrypt string,
) bool {
	if !wc.HasCryptoEnabled() {
		return true // Skip verification when not configured.
	}

	expected := wc.calcSignature(timestamp, nonce, encrypt)
	return strings.EqualFold(expected, msgSignature)
}

// VerifySignature validates the msg_signature from callback POST
// body containing an encrypted XML payload.
func (wc *WeComCrypto) VerifySignature(
	msgSignature, timestamp, nonce string, body []byte,
) error {
	if !wc.HasCryptoEnabled() {
		return nil // Skip verification when not configured.
	}

	if len(body) == 0 {
		return fmt.Errorf("wecom: empty callback body")
	}

	expected := wc.calcSignature(timestamp, nonce, string(body))
	if !strings.EqualFold(expected, msgSignature) {
		return fmt.Errorf("wecom: signature mismatch")
	}

	return nil
}

// DecryptEchostr decrypts the echostr parameter from URL verification.
// Returns the plaintext challenge string to echo back.
func (wc *WeComCrypto) DecryptEchostr(echostr string) (string, error) {
	return wc.decrypt(echostr)
}

// DecryptMessage decrypts the <Encrypt> portion of an encrypted
// WeCom callback message. Returns the plaintext XML.
func (wc *WeComCrypto) DecryptMessage(encrypt string) ([]byte, error) {
	plaintext, err := wc.decrypt(encrypt)
	if err != nil {
		return nil, err
	}
	return []byte(plaintext), nil
}

// calcSignature computes the SHA1 signature as defined by WeCom:
//
//	sorted_params = Sort(Token, Timestamp, Nonce, Encrypt)
//	sha1(sorted_params) → hex string
func (wc *WeComCrypto) calcSignature(
	timestamp, nonce, encrypt string,
) string {
	strs := []string{wc.token, timestamp, nonce, encrypt}
	sort.Strings(strs)

	h := sha1.New()
	h.Write([]byte(strings.Join(strs, "")))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// decrypt decrypts a Base64-encoded AES-256-CBC ciphertext and
// extracts the plaintext message.
//
// Encrypted payload format (before Base64):
//
//	random(16) + msg_len(4, big-endian) + msg + corpid
//
// After decrypting, we strip the random prefix and verify the
// trailing corpid.
func (wc *WeComCrypto) decrypt(ciphertextB64 string) (string, error) {
	if !wc.HasCryptoEnabled() {
		return "", fmt.Errorf("wecom: encryption not configured")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("wecom: base64 decode: %w", err)
	}

	plaintext, err := aesDecrypt(ciphertext, wc.encodingAESKey)
	if err != nil {
		return "", fmt.Errorf("wecom: aes decrypt: %w", err)
	}

	// Strip PKCS7 padding.
	plaintext, err = pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", fmt.Errorf("wecom: pkcs7 unpad: %w", err)
	}

	// Parse: random(16) + msg_len(4) + msg + corpid.
	const headerLen = 20 // random16 + msg_len4
	if len(plaintext) < headerLen {
		return "", fmt.Errorf("wecom: plaintext too short")
	}

	msgLen := binary.BigEndian.Uint32(
		plaintext[16:20],
	)
	msgStart := headerLen
	msgEnd := headerLen + int(msgLen)
	if msgEnd > len(plaintext) {
		return "", fmt.Errorf("wecom: message length overflow")
	}

	msg := string(plaintext[msgStart:msgEnd])
	corpID := string(plaintext[msgEnd:])

	if wc.corpID != "" && corpID != wc.corpID {
		return "", fmt.Errorf(
			"wecom: corpid mismatch: expected %s, got %s",
			wc.corpID, corpID)
	}

	return msg, nil
}

// aesDecrypt performs AES-256-CBC decryption with the given key.
// IV is the first 16 bytes of the key.
func aesDecrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("wecom: create cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("wecom: ciphertext too short")
	}

	iv := key[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	return plaintext, nil
}

// pkcs7Unpad removes PKCS7 padding from decrypted data.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("wecom: empty data")
	}
	if len(data)%blockSize != 0 {
		return nil, fmt.Errorf(
			"wecom: data not aligned to block size")
	}

	padLen := int(data[len(data)-1])
	if padLen > blockSize || padLen == 0 {
		return nil, fmt.Errorf(
			"wecom: invalid padding length: %d", padLen)
	}

	// Verify all padding bytes are correct.
	for i := range padLen {
		if data[len(data)-1-i] != byte(padLen) {
			return nil, fmt.Errorf(
				"wecom: invalid padding byte")
		}
	}

	return data[:len(data)-padLen], nil
}

// encryptMessage encrypts a plaintext message for WeCom reply.
// Returns the Base64-encoded ciphertext suitable for xml.Encrypt.
//
// Encrypted payload format:
//
//	random(16) + msg_len(4, big-endian) + msg + corpid
//
// followed by PKCS7 padding and AES-256-CBC encryption.
//
// TODO: Used for encrypted passive reply in enterprise app mode.
//
//nolint:unused
func (wc *WeComCrypto) encryptMessage(msg []byte) (string, error) {
	if !wc.HasCryptoEnabled() {
		return "", fmt.Errorf("wecom: encryption not configured")
	}

	// Build plaintext: random16 + msg_len4 + msg + corpid.
	const randomLen = 16
	plaintext := make([]byte, randomLen+4+len(msg)+len(wc.corpID))

	// Random prefix (use a deterministic prefix in production,
	// here we use zeroes for simplicity; real impl should use
	// crypto/rand).
	copy(plaintext[:randomLen], make([]byte, randomLen))

	binary.BigEndian.PutUint32(
		plaintext[randomLen:randomLen+4],
		uint32(len(msg)),
	)
	copy(plaintext[randomLen+4:], msg)
	copy(plaintext[randomLen+4+len(msg):], wc.corpID)

	// PKCS7 pad.
	padded := pkcs7Pad(plaintext, aes.BlockSize)

	// AES-256-CBC encrypt.
	ciphertext, err := aesEncrypt(padded, wc.encodingAESKey)
	if err != nil {
		return "", fmt.Errorf("wecom: encrypt: %w", err)
	}

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// aesEncrypt performs AES-256-CBC encryption.
func aesEncrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("wecom: create cipher: %w", err)
	}

	iv := key[:aes.BlockSize]
	mode := cipher.NewCBCEncrypter(block, iv)
	ciphertext := make([]byte, len(plaintext))
	mode.CryptBlocks(ciphertext, plaintext)

	return ciphertext, nil
}

// pkcs7Pad adds PKCS7 padding.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	padding := bytes.Repeat([]byte{byte(padLen)}, padLen)
	return append(data, padding...)
}

// parseCallbackParams extracts msg_signature, timestamp, nonce, and
// optional echostr from URL query parameters.
func parseCallbackParams(q url.Values) (sig, ts, nonce, echostr string) {
	sig = q.Get("msg_signature")
	ts = q.Get("timestamp")
	nonce = q.Get("nonce")
	echostr = q.Get("echostr")
	return
}
