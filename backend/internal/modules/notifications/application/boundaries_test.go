package application_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"mendry/backend/internal/modules/notifications/domain"
)

func TestChannelNamesUseCharacterBounds(t *testing.T) {
	svc, _, _ := service(t)
	request := input()
	request.Name = strings.Repeat("\u754c", 100)
	if _, err := svc.SaveChannel(context.Background(), uuid.NewString(), "", request); err != nil {
		t.Fatalf("100 unicode characters rejected: %v", err)
	}
	request.Name += "x"
	if _, err := svc.SaveChannel(context.Background(), uuid.NewString(), "", request); err == nil {
		t.Fatal("101 characters accepted")
	}
}
func TestWorkerRejectsMalformedCredentialEnvelopeWithoutPanicking(t *testing.T) {
	svc, store, out := service(t)
	store.delivery = &domain.Delivery{ID: uuid.NewString(), ProjectID: uuid.NewString(), ChannelID: uuid.NewString(), Platform: "telegram", Ciphertext: make([]byte, 17), Nonce: []byte("bad")}
	processed, err := svc.DeliverOne(context.Background())
	if err != nil || !processed || store.finishedError == nil || out.calls != 0 {
		t.Fatalf("invalid nonce accepted: processed=%t err=%v", processed, err)
	}
}
