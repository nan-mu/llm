package babeldoc

import (
	"context"

	"encore.dev/pubsub"
	"encore.dev/rlog"
)

const (
	statusPending = "pending"
	statusDown    = "down"
	statusError   = "error"
)

// TranslateEvent asks for a PDF translation. The subscription does not run BabelDOC.
type TranslateEvent struct {
	Hash string `json:"hash"`
}

// TranslateTopic is the babeldoc-translate topic from the architecture contract.
var TranslateTopic = pubsub.NewTopic[*TranslateEvent]("babeldoc-translate", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

// The worker subscription acknowledges the event and does not shell out to pixi or babeldoc.
var _ = pubsub.NewSubscription(TranslateTopic, "babeldoc-translate-worker", pubsub.SubscriptionConfig[*TranslateEvent]{
	Handler: func(ctx context.Context, event *TranslateEvent) error {
		hash := ""
		if event != nil {
			hash = event.Hash
		}
		rlog.Info("babeldoc worker not wired", "hash", hash)
		return nil
	},
})
