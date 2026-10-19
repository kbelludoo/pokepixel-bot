package game

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestClientHMACSigning(t *testing.T) {
	cb := NewBot(AccountConfig{ID: "test"}).cb
	client := NewGameClient("", cb)
	key := "test-secret-signing-key-12345"
	client.SetSigningKey(key)

	method := "POST"
	path := "/hunts/prepare?extra=123"
	timestamp := int64(1792364400000)
	body := `{"zone_id":"zone_grass_easy"}`

	sig, err := client.signRequest(method, path, timestamp, body)
	if err != nil {
		t.Fatalf("signRequest falhou: %v", err)
	}

	// Calcula manualmente para verificar paridade exata com o cliente JS
	h := sha256.New()
	h.Write([]byte(body))
	digest := hex.EncodeToString(h.Sum(nil))

	expectedPayload := fmt.Sprintf("%s\n%s\n%d\n%s", method, "/hunts/prepare", timestamp, digest)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(expectedPayload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if sig != expectedSig {
		t.Errorf("Assinatura gerada (%s) diferente da esperada (%s)", sig, expectedSig)
	}
}
