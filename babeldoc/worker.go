package babeldoc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"encore.dev/pubsub"
	"encore.dev/rlog"
)

// TranslateEvent asks the worker to process one document hash.
type TranslateEvent struct {
	Hash string `json:"hash"`
}

// TranslateTopic wakes the serial translation worker.
var TranslateTopic = pubsub.NewTopic[*TranslateEvent]("babeldoc-translate", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

var _ = pubsub.NewSubscription(
	TranslateTopic, "run-translate",
	pubsub.SubscriptionConfig[*TranslateEvent]{
		Handler:        pubsub.MethodHandler((*Service).handleTranslate),
		MaxConcurrency: 1,
		RetryPolicy: &pubsub.RetryPolicy{
			MaxRetries: 3,
			MinBackoff: 10 * time.Second,
			MaxBackoff: 5 * time.Minute,
		},
	},
)

func (s *Service) handleTranslate(ctx context.Context, ev *TranslateEvent) error {
	if ev == nil || !validHash(ev.Hash) {
		rlog.Error("babeldoc translate bad event", "event", "babeldoc.bad_event")
		return nil
	}
	hash := ev.Hash
	meta, err := getMeta(ctx, hash)
	if err != nil {
		return err
	}
	if meta == nil {
		rlog.Info("babeldoc translate skip missing", "event", "babeldoc.skip_missing", "hash_prefix", shortHash(hash))
		return nil
	}
	if meta.Status != statusPending {
		rlog.Info("babeldoc translate skip status", "event", "babeldoc.skip_status",
			"hash_prefix", shortHash(hash), "status", meta.Status)
		return nil
	}

	claimed, err := claimPending(ctx, hash)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	runCtx, cancel := context.WithTimeout(context.Background(), s.translateTimeout)
	defer cancel()

	if err := s.runOne(runCtx, hash); err != nil {
		rlog.Error("babeldoc translate failed", "event", "babeldoc.translate_failed",
			"hash_prefix", shortHash(hash), "err", err)
		if markErr := markError(context.Background(), hash, "TRANSLATE_FAILED", truncateErr(err)); markErr != nil {
			rlog.Error("babeldoc mark error failed", "event", "babeldoc.mark_error_failed",
				"hash_prefix", shortHash(hash), "err", markErr)
			return markErr
		}
		// Business failure persisted — do not retry forever.
		return nil
	}
	rlog.Info("babeldoc translate ok", "event", "babeldoc.translate_ok", "hash_prefix", shortHash(hash))
	return nil
}

func (s *Service) runOne(ctx context.Context, hash string) error {
	source, err := getSourcePDF(ctx, hash)
	if err != nil {
		return err
	}
	if len(source) == 0 {
		return fmt.Errorf("empty source pdf")
	}

	dir := s.workDir(hash)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			rlog.Error("babeldoc tmp cleanup failed", "event", "babeldoc.tmp_cleanup_failed",
				"hash_prefix", shortHash(hash), "err", rmErr)
		}
	}()

	sourcePath := filepath.Join(dir, "source.pdf")
	if err := os.WriteFile(sourcePath, source, 0o644); err != nil {
		return err
	}
	outDir := filepath.Join(dir, "out")
	dualPath, err := s.runner.Run(ctx, s.babelDOCRoot, s.configFile, sourcePath, outDir)
	if err != nil {
		return err
	}
	dual, err := os.ReadFile(dualPath)
	if err != nil {
		return err
	}
	if !looksLikePDF(dual) {
		return fmt.Errorf("dual artifact is not a pdf")
	}
	dualSHA := sha256Bytes(dual)
	return markDown(ctx, hash, dual, dualSHA)
}

func truncateErr(err error) string {
	msg := err.Error()
	if len(msg) > 500 {
		return msg[:500]
	}
	return msg
}
