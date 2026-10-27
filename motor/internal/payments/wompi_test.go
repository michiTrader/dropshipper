package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Note: the checksum printed in Wompi's documentation example
// (3476DDA5…) is illustrative — the docs say "yields, for example" — and is
// NOT the real SHA256 of the documented string. So these tests compute the
// checksum with the documented algorithm instead of trusting that literal:
// values of signature.properties (in order) + timestamp + events secret.

const testSecret = "test_events_secret_123"

func checksumFor(concat string) string {
	sum := sha256.Sum256([]byte(concat))
	return hex.EncodeToString(sum[:])
}

func eventJSON(checksum string) string {
	return `{
  "event": "transaction.updated",
  "data": {"transaction": {"id": "1234-1610641025-49201", "amount_in_cents": 4490000, "reference": "PED7-1234",
    "currency": "COP", "payment_method_type": "NEQUI", "status": "APPROVED", "payment_link_id": "test_abc"}},
  "environment": "test",
  "signature": {"properties": ["transaction.id", "transaction.status", "transaction.amount_in_cents"],
    "checksum": "` + checksum + `"},
  "timestamp": 1530291411,
  "sent_at": "2018-07-20T16:45:05.000Z"
}`
}

func validEvent() string {
	return eventJSON(checksumFor("1234-1610641025-49201" + "APPROVED" + "4490000" + "1530291411" + testSecret))
}

func TestParseEventValid(t *testing.T) {
	w := &Wompi{EventsSecret: testSecret}
	ev, err := w.ParseEvent([]byte(validEvent()), "")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != "APPROVED" || ev.AmountInCents != 4490000 || ev.PaymentLinkID != "test_abc" || ev.Reference != "PED7-1234" || ev.Method != "NEQUI" {
		t.Fatalf("%+v", ev)
	}
}

func TestParseEventAcceptsUppercaseHeaderChecksum(t *testing.T) {
	sum := checksumFor("1234-1610641025-49201" + "APPROVED" + "4490000" + "1530291411" + testSecret)
	w := &Wompi{EventsSecret: testSecret}
	if _, err := w.ParseEvent([]byte(eventJSON("ignored")), strings.ToUpper(sum)); err != nil {
		t.Fatalf("X-Event-Checksum header (uppercase) must be accepted: %v", err)
	}
}

func TestParseEventRejectsWrongSecret(t *testing.T) {
	w := &Wompi{EventsSecret: "otro_secreto"}
	if _, err := w.ParseEvent([]byte(validEvent()), ""); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("expected bad checksum, got %v", err)
	}
}

func TestParseEventRejectsTamperedAmount(t *testing.T) {
	tampered := strings.Replace(validEvent(), `"amount_in_cents": 4490000`, `"amount_in_cents": 100`, 1)
	w := &Wompi{EventsSecret: testSecret}
	if _, err := w.ParseEvent([]byte(tampered), ""); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("a changed amount must break the checksum, got %v", err)
	}
}

func TestParseEventRequiresSecret(t *testing.T) {
	w := &Wompi{}
	if _, err := w.ParseEvent([]byte(validEvent()), ""); err == nil {
		t.Fatal("events without a configured secret must be refused")
	}
}
