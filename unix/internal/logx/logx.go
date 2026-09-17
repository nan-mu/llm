package logx

import (
	"fmt"
	"log"
	"strings"

	"encore.dev/rlog"
)

// Info writes to stderr always, and to Encore rlog when the runtime is present.
func Info(msg string, keysAndValues ...any) {
	log.Print(format(msg, keysAndValues...))
	defer func() { _ = recover() }()
	rlog.Info(msg, keysAndValues...)
}

func format(msg string, keysAndValues ...any) string {
	if len(keysAndValues) == 0 {
		return msg
	}
	parts := make([]string, 0, len(keysAndValues)/2)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		parts = append(parts, fmt.Sprintf("%v=%v", keysAndValues[i], keysAndValues[i+1]))
	}
	return msg + " " + strings.Join(parts, " ")
}
