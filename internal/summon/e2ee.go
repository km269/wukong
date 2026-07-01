// Package summon provides end-to-end encrypted (E2EE) agent-to-agent
// messaging based on ANP message profiles (P1-P9).
//
// E2EE ensures that agent-to-agent messages remain confidential
// and tamper-proof, even when passing through intermediate services.
// It uses X25519 key agreement (derived from did:wba key-2) with
// ChaCha20-Poly1305 authenticated encryption.
//
// Message Profiles supported:
//
//	P1 - JSON-RPC 2.0 Core Binding (request/response/error)
//	P3 - Direct Messaging Base Semantics
//	P5 - Direct Message E2EE Overlay
//	P7 - Attachments and Object Transfer
package summon

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/km269/wukong/internal/ard"
	"github.com/km269/wukong/internal/util"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// ============================================================================
// E2EE Messenger
// ============================================================================

// E2EEMessenger provides end-to-end encrypted agent-to-agent
// messaging using X25519 key agreement and ChaCha20-Poly1305
// authenticated encryption.
//
// The messenger uses DID:wba keys for identity-bound encryption:
//   - Signing key (key-1, Ed25519) — message signing for authenticity
//   - Agreement key (key-2, X25519) — key agreement for E2EE
type E2EEMessenger struct {
	mu sync.RWMutex

	// DID identity manager
	didManager *ard.DIDManager

	// Local identity
	localDID string

	// Per-remote session cache
	sessions map[string]*E2EESession

	// Signer for message authenticity
	signer *ard.HTTPSigner
}

// E2EESession holds the cryptographic material for an established
// E2EE session with a remote agent.
type E2EESession struct {
	// SessionID uniquely identifies this session.
	SessionID string

	// RemoteDID is the remote agent's DID.
	RemoteDID string

	// SharedSecret is the ECDH-derived shared symmetric key.
	SharedSecret []byte

	// EncryptKey is the derived ChaCha20-Poly1305 encryption key.
	EncryptKey []byte

	// CreatedAt is when the session was established.
	CreatedAt time.Time

	// ExpiresAt is when the session keys should be rotated.
	ExpiresAt time.Time
}

// E2EEMessengerConfig configures the E2EE messenger.
type E2EEMessengerConfig struct {
	// DIDManager provides identity and cryptographic keys.
	DIDManager *ard.DIDManager

	// SessionTTL is the key rotation interval.
	// Default: 1 hour.
	SessionTTL time.Duration
}

// NewE2EEMessenger creates a new E2EE messenger backed by
// the DID manager's X25519 agreement key.
func NewE2EEMessenger(cfg *E2EEMessengerConfig) *E2EEMessenger {
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 1 * time.Hour
	}

	return &E2EEMessenger{
		didManager: cfg.DIDManager,
		localDID:   cfg.DIDManager.DID(),
		sessions:   make(map[string]*E2EESession),
		signer:     ard.NewHTTPSigner(cfg.DIDManager),
	}
}

// ============================================================================
// Encrypted Message Types
// ============================================================================

// EncryptedMessage is a P5 (E2EE Overlay) encrypted message envelope.
// It wraps a JSON-RPC 2.0 message with encryption and authentication.
type EncryptedMessage struct {
	// Version is the encryption protocol version.
	Version string `json:"version"`

	// SessionID identifies the E2EE session.
	SessionID string `json:"sessionId"`

	// Ciphertext is the base64-encoded ChaCha20-Poly1305 ciphertext.
	Ciphertext string `json:"ciphertext"`

	// Nonce is the base64-encoded 12-byte nonce for decryption.
	Nonce string `json:"nonce"`

	// SenderDID is the sender's DID for authentication.
	SenderDID string `json:"senderDID"`

	// RecipientDID is the intended recipient's DID.
	RecipientDID string `json:"recipientDID"`

	// Timestamp is the message creation time (ISO 8601).
	Timestamp string `json:"timestamp"`
}

// E2EEMessageReceipt confirms delivery of an encrypted message.
type E2EEMessageReceipt struct {
	// MessageID is the original message identifier.
	MessageID string `json:"messageId"`

	// DeliveredAt indicates when delivery was confirmed.
	DeliveredAt string `json:"deliveredAt"`

	// Status is the delivery status: "delivered", "expired",
	// "rejected".
	Status string `json:"status"`
}

// ============================================================================
// Session Establishment
// ============================================================================

// EstablishSession creates a new E2EE session with a remote agent
// using X25519 ECDH key agreement.
//
// The flow:
//  1. Resolve remote DID document to get key-2 (X25519 public key)
//  2. Perform ECDH with local key-2 and remote key-2
//  3. Derive shared symmetric key via HKDF
//  4. Cache the session for subsequent messages
func (m *E2EEMessenger) EstablishSession(
	ctx context.Context,
	remoteDID string,
	httpClient *ard.SignedHTTPClient,
) (*E2EESession, error) {
	// 1. Resolve remote DID document
	doc, err := ard.ResolveDID(nil, remoteDID)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: resolve remote DID: %w", err)
	}

	// 2. Extract remote X25519 public key (key-2)
	remotePub, err := extractX25519Key(doc)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: extract remote key-2: %w", err)
	}

	// 3. Perform ECDH
	localPriv := m.didManager.AgreementPrivateKey()
	remotePubKey, err := ecdh.X25519().NewPublicKey(remotePub)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: invalid remote public key: %w", err)
	}
	sharedSecret, err := localPriv.ECDH(remotePubKey)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: ECDH failed: %w", err)
	}

	// 4. Derive encryption key via HKDF
	encryptKey := deriveKey(sharedSecret, []byte("wukong-e2ee-v1"))

	// 5. Create session
	session := &E2EESession{
		SessionID:    uuid.New().String(),
		RemoteDID:    remoteDID,
		SharedSecret: sharedSecret,
		EncryptKey:   encryptKey,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	m.mu.Lock()
	m.sessions[remoteDID] = session
	m.mu.Unlock()

	util.Logger.Info("E2EE: session established",
		slog.String("session_id", session.SessionID),
		slog.String("remote_did", remoteDID),
	)

	return session, nil
}

// ============================================================================
// Message Encryption & Decryption
// ============================================================================

// EncryptAndSend encrypts a plaintext payload and wraps it as an
// ANP P5 EncryptedMessage envelope.
func (m *E2EEMessenger) EncryptAndSend(
	ctx context.Context,
	session *E2EESession,
	recipientDID string,
	payload json.RawMessage,
) (*EncryptedMessage, error) {
	if session == nil {
		return nil, fmt.Errorf(
			"e2ee: session is required")
	}

	// Generate random 12-byte nonce
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf(
			"e2ee: generate nonce: %w", err)
	}

	// Create AEAD cipher
	aead, err := chacha20poly1305.NewX(session.EncryptKey)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: create cipher: %w", err)
	}

	// Encrypt with ChaCha20-Poly1305
	ciphertext := aead.Seal(nil, nonce, payload, nil)

	msg := &EncryptedMessage{
		Version:      "1.0",
		SessionID:    session.SessionID,
		Ciphertext:   base64.RawURLEncoding.EncodeToString(ciphertext),
		Nonce:        base64.RawURLEncoding.EncodeToString(nonce),
		SenderDID:    m.localDID,
		RecipientDID: recipientDID,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}

	return msg, nil
}

// DecryptMessage decrypts an incoming EncryptedMessage envelope
// and returns the plaintext JSON payload.
func (m *E2EEMessenger) DecryptMessage(
	msg *EncryptedMessage,
) (json.RawMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf(
			"e2ee: message is nil")
	}

	// Get or establish session
	session, ok := m.getSession(msg.SenderDID)
	if !ok {
		return nil, fmt.Errorf(
			"e2ee: no session with %s", msg.SenderDID)
	}

	if session.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf(
			"e2ee: session expired")
	}

	// Decode nonce
	nonce, err := base64.RawURLEncoding.DecodeString(msg.Nonce)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: decode nonce: %w", err)
	}

	if len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, fmt.Errorf(
			"e2ee: invalid nonce length: %d", len(nonce))
	}

	// Decode ciphertext
	ciphertext, err := base64.RawURLEncoding.DecodeString(
		msg.Ciphertext,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: decode ciphertext: %w", err)
	}

	// Create AEAD cipher
	aead, err := chacha20poly1305.NewX(session.EncryptKey)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: create cipher: %w", err)
	}

	// Decrypt
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf(
			"e2ee: decrypt failed (wrong key or tampered): %w", err)
	}

	return json.RawMessage(plaintext), nil
}

// ============================================================================
// Session Management
// ============================================================================

// getSession retrieves an established E2EE session by remote DID.
func (m *E2EEMessenger) getSession(
	remoteDID string,
) (*E2EESession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[remoteDID]
	return session, ok
}

// RemoveSession removes an E2EE session.
func (m *E2EEMessenger) RemoveSession(remoteDID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, remoteDID)
}

// CleanExpiredSessions removes all expired E2EE sessions.
func (m *E2EEMessenger) CleanExpiredSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for did, session := range m.sessions {
		if now.After(session.ExpiresAt) {
			delete(m.sessions, did)
		}
	}
}

// ActiveSessions returns the count of active E2EE sessions.
func (m *E2EEMessenger) ActiveSessions() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ============================================================================
// Cryptographic Helpers
// ============================================================================

// deriveKey derives a 32-byte ChaCha20-Poly1305 key from a shared
// secret using HKDF-SHA256.
func deriveKey(sharedSecret, info []byte) []byte {
	hkdfReader := hkdf.New(
		sha256.New,
		sharedSecret,
		nil,   // No salt
		info,  // Application-specific context
	)

	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := hkdfReader.Read(key); err != nil {
		// In practice, hkdf.Read never errors when reading
		// within the hash output length.
		panic(fmt.Sprintf("e2ee: hkdf failed: %v", err))
	}

	return key
}

// extractX25519Key extracts the X25519 public key (key-2)
// from a DID document.
func extractX25519Key(doc *ard.DIDDocument) ([]byte, error) {
	for i := range doc.VerificationMethod {
		vm := &doc.VerificationMethod[i]
		if vm.PublicKeyJWK.CRV == ard.DIDWBAJWKCrvX25519 &&
			vm.PublicKeyJWK.KTY == ard.DIDWBAJWKType {
			return base64.RawURLEncoding.DecodeString(
				vm.PublicKeyJWK.X,
			)
		}
	}
	return nil, fmt.Errorf(
		"X25519 key-2 not found in DID document")
}
