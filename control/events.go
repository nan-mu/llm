package control

import (
	"context"
	"encoding/json"

	"encore.dev/rlog"
)

const (
	eventLoad            = "LOAD"
	eventLoaded          = "LOADED"
	eventLoadFailed      = "LOAD_FAILED"
	eventUnload          = "UNLOAD"
	eventUnloaded        = "UNLOADED"
	eventUnloadFailed    = "UNLOAD_FAILED"
	eventReconcileStart  = "RECONCILE_START"
	eventReconcileDone   = "RECONCILE_DONE"
	eventReconcileFailed = "RECONCILE_FAILED"
	eventFrontendStart   = "FRONTEND_START"
	eventFrontendStop    = "FRONTEND_STOP"
)

func recordEvent(ctx context.Context, model, event string, details map[string]string) {
	ctx = context.WithoutCancel(ctx)
	var modelArg any
	if model != "" {
		modelArg = model
	}
	var detailsArg any
	if len(details) > 0 {
		b, err := json.Marshal(details)
		if err != nil {
			rlog.Error("marshal model event details failed",
				"event", "control.event_marshal_failed",
				"err", err,
			)
			return
		}
		detailsArg = json.RawMessage(b)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO model_events (id, model, event, details)
		VALUES (gen_random_uuid(), $1, $2, $3)
	`, modelArg, event, detailsArg); err != nil {
		rlog.Error("record model event failed",
			"event", "control.event_write_failed",
			"model_event", event,
			"err", err,
		)
	}
}
