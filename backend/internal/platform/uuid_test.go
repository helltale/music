package platform

import "testing"

func TestNewUUID(t *testing.T) {
	a, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !uuidPattern.MatchString(a) || !uuidPattern.MatchString(b) {
		t.Fatalf("ids %s %s", a, b)
	}
	if a == b {
		t.Fatal("expected distinct ids")
	}
}
