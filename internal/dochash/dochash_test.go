package dochash

import "testing"

func TestSourceID(t *testing.T) {
	got := SourceID([]byte("abc"))
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("SourceID = %s, want %s", got, want)
	}
	if !Valid(got) {
		t.Fatalf("SourceID %s is not a valid task id", got)
	}
}

func TestValid(t *testing.T) {
	if Valid("ABC") || Valid("") {
		t.Fatal("expected invalid ids")
	}
}

func TestLooksLikePDF(t *testing.T) {
	if !LooksLikePDF([]byte("%PDF-1.7")) {
		t.Fatal("expected pdf header")
	}
	if LooksLikePDF([]byte("abc")) {
		t.Fatal("expected non-pdf")
	}
}
