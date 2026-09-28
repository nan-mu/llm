package babeldoc

import (
	"context"
	"testing"
	"time"

	"encore.dev/et"
)

func stubService(t *testing.T, dual []byte, runErr error) *Service {
	t.Helper()
	svc := &Service{
		runner: &StubRunner{
			DualPDF: dual,
			Err:     runErr,
		},
		babelDOCRoot:     t.TempDir(),
		workRoot:         t.TempDir(),
		maxUploadBytes:   defaultMaxUploadBytes,
		translateTimeout: time.Minute,
		configFile:       "babeldoc.toml",
	}
	et.MockService("babeldoc", svc)
	return svc
}

func samplePDF(label string) []byte {
	return []byte("%PDF-1.4\n%" + label + "\n%%EOF\n")
}

func TestUpsertIdempotentAndTranslateDown(t *testing.T) {
	ctx := context.Background()
	dual := samplePDF("dual")
	svc := stubService(t, dual, nil)

	src := samplePDF("source-a")
	hash := sha256Bytes(src)

	res, err := UpsertSource(ctx, &UpsertSourceParams{ClientSHA256: hash, PDF: src})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Status != statusPending || res.ID != hash {
		t.Fatalf("upsert: %+v", res)
	}

	again, err := UpsertSource(ctx, &UpsertSourceParams{ClientSHA256: hash, PDF: src})
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Status != statusPending {
		t.Fatalf("second upsert should not create: %+v", again)
	}

	msgs := et.Topic(TranslateTopic).PublishedMessages()
	if len(msgs) < 1 {
		t.Fatal("expected translate publish")
	}

	if err := svc.handleTranslate(ctx, &TranslateEvent{Hash: hash}); err != nil {
		t.Fatal(err)
	}

	file, err := GetDual(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !file.Found || !file.Ready || file.ArtifactSHA != sha256Bytes(dual) {
		t.Fatalf("dual: %+v", file)
	}

	list, err := ListDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 1 || list.Documents[0].Status != statusDown {
		t.Fatalf("list: %+v", list)
	}
}

func TestHashMismatch(t *testing.T) {
	ctx := context.Background()
	stubService(t, nil, nil)
	src := samplePDF("mismatch")
	res, err := UpsertSource(ctx, &UpsertSourceParams{
		ClientSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PDF:          src,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != "HASH_MISMATCH" {
		t.Fatalf("want HASH_MISMATCH got %+v", res)
	}
}

func TestTranslateErrorAndRetry(t *testing.T) {
	ctx := context.Background()
	svc := stubService(t, nil, errStubFail{})

	src := samplePDF("fail-me")
	hash := sha256Bytes(src)
	if _, err := UpsertSource(ctx, &UpsertSourceParams{ClientSHA256: hash, PDF: src}); err != nil {
		t.Fatal(err)
	}
	if err := svc.handleTranslate(ctx, &TranslateEvent{Hash: hash}); err != nil {
		t.Fatal(err)
	}
	meta, err := getMeta(ctx, hash)
	if err != nil || meta == nil || meta.Status != statusError {
		t.Fatalf("meta: %+v err=%v", meta, err)
	}

	// Ordinary upsert must not implicit-retry.
	again, err := UpsertSource(ctx, &UpsertSourceParams{ClientSHA256: hash, PDF: src})
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != statusError || again.Created {
		t.Fatalf("upsert on error: %+v", again)
	}

	svc.runner = &StubRunner{DualPDF: samplePDF("recovered")}
	out, err := Retry(ctx, &RetryParams{Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || !out.Requeued || out.Status != statusPending {
		t.Fatalf("retry: %+v", out)
	}
	if err := svc.handleTranslate(ctx, &TranslateEvent{Hash: hash}); err != nil {
		t.Fatal(err)
	}
	file, err := GetDual(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !file.Ready {
		t.Fatalf("expected down after retry: %+v", file)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	stubService(t, samplePDF("d"), nil)
	src := samplePDF("del")
	hash := sha256Bytes(src)
	if _, err := UpsertSource(ctx, &UpsertSourceParams{ClientSHA256: hash, PDF: src}); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteDocument(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteDocument(ctx, hash); err != nil {
		t.Fatal(err)
	}
	file, err := GetDual(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if file.Found {
		t.Fatal("expected missing after delete")
	}
}

type errStubFail struct{}

func (errStubFail) Error() string { return "stub translate failed" }
