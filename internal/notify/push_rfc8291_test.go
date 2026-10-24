package notify

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"testing"

	"golang.org/x/crypto/hkdf"
)

// decryptAES128GCM is an independent RFC 8291 / RFC 8188 decoder, written the
// way browsers (Chrome/Firefox/Edge) decrypt Web Push messages. It is used to
// verify that encryptPayload produces messages a real user agent can read.
func decryptAES128GCM(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	asPubBytes := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	if uint32(len(ciphertext)) > rs {
		t.Fatalf("record (%d bytes) larger than rs (%d)", len(ciphertext), rs)
	}

	asPub, err := ecdh.P256().NewPublicKey(asPubBytes)
	if err != nil {
		t.Fatalf("keyid is not a P-256 public key: %v", err)
	}
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	uaPub := uaPriv.PublicKey().Bytes()

	expand := func(secret, salt, info []byte, n int) []byte {
		out := make([]byte, n)
		if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), out); err != nil {
			t.Fatalf("hkdf: %v", err)
		}
		return out
	}
	// RFC 8291 section 3.4
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPub...), asPubBytes...)
	ikm := expand(shared, authSecret, keyInfo, 32)
	// RFC 8188 section 2.2 / 2.3
	cek := expand(ikm, salt, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := expand(ikm, salt, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("AES-GCM decryption failed (browser would drop the message): %v", err)
	}
	// Strip RFC 8188 padding: last record ends with 0x02 followed by zero bytes.
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 0x02 {
		t.Fatalf("missing 0x02 last-record delimiter")
	}
	return plain[:i]
}

func TestEncryptPayload_DecryptableByUserAgent(t *testing.T) {
	t.Parallel()
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("auth secret: %v", err)
	}
	payload := []byte(`{"title":"[onWatch] Test Push","body":"Push notifications are working correctly."}`)

	body, err := encryptPayload(payload, uaPriv.PublicKey().Bytes(), authSecret)
	if err != nil {
		t.Fatalf("encryptPayload: %v", err)
	}
	got := decryptAES128GCM(t, body, uaPriv, authSecret)
	if !bytes.Equal(got, payload) {
		t.Fatalf("decrypted payload = %q, want %q", got, payload)
	}
}
