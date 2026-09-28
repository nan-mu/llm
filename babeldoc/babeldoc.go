// Package babeldoc owns PDF translation tasks (Postgres + local BabelDOC via pixi).
package babeldoc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

const (
	statusPending = "pending"
	statusDown    = "down"
	statusError   = "error"

	defaultBabelDOCRoot       = "/Users/nan/BabelDOC"
	defaultWorkRoot           = "/tmp"
	defaultMaxUploadBytes     = 100 << 20 // 100 MiB
	defaultTranslateTimeout   = 12 * time.Hour
	defaultConfigFile         = "babeldoc.toml"
)

//encore:service
type Service struct {
	runner             Runner
	babelDOCRoot       string
	workRoot           string
	maxUploadBytes     int64
	translateTimeout   time.Duration
	configFile         string
}

var db = sqldb.NewDatabase("babeldoc", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func initService() (*Service, error) {
	s := &Service{
		runner:           newPixiRunner(),
		babelDOCRoot:     envOr("BABELDOC_ROOT", defaultBabelDOCRoot),
		workRoot:         envOr("BABELDOC_WORK_ROOT", defaultWorkRoot),
		maxUploadBytes:   defaultMaxUploadBytes,
		translateTimeout: defaultTranslateTimeout,
		configFile:       defaultConfigFile,
	}
	if v := os.Getenv("BABELDOC_MAX_UPLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			s.maxUploadBytes = n
		}
	}
	go s.requeuePending(context.Background())
	return s, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (s *Service) requeuePending(ctx context.Context) {
	// Small delay so pubsub subscription is ready after boot.
	time.Sleep(2 * time.Second)
	ids, err := listPendingIDs(ctx)
	if err != nil {
		rlog.Error("babeldoc requeue list failed", "event", "babeldoc.requeue_list_failed", "err", err)
		return
	}
	for _, id := range ids {
		if _, err := TranslateTopic.Publish(ctx, &TranslateEvent{Hash: id}); err != nil {
			rlog.Error("babeldoc requeue publish failed", "event", "babeldoc.requeue_publish_failed",
				"hash_prefix", shortHash(id), "err", err)
			continue
		}
		rlog.Info("babeldoc requeued pending", "event", "babeldoc.requeued", "hash_prefix", shortHash(id))
	}
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

func (s *Service) workDir(hash string) string {
	return filepath.Join(s.workRoot, "babeldoc-"+hash)
}
