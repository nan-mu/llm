package control

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"encore.app/internal/modelstate"
	"encore.dev/rlog"
)

const (
	memorySampleInterval = 10 * time.Second
	memorySampleKeep     = 24 * 360 // 8640
)

// memoryMBFn sums process memory for pids and returns megabytes. Overridable in tests.
var memoryMBFn = sumProcessMemoryMB

var (
	footprintHeaderRe = regexp.MustCompile(`(?i)Footprint:\s*([0-9]+(?:\.[0-9]+)?)\s*(KB|MB|GB)`)
	physFootprintRe   = regexp.MustCompile(`(?i)phys_footprint:\s*([0-9]+(?:\.[0-9]+)?)\s*(KB|MB|GB)`)
)

func (s *Service) startMemorySampler(ctx context.Context) {
	go func() {
		t := time.NewTicker(memorySampleInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sampleAllFrontends(ctx)
			}
		}
	}()
}

func (s *Service) sampleAllFrontends(ctx context.Context) {
	for _, kind := range modelstate.AllFrontends() {
		if err := s.sampleFrontend(ctx, kind); err != nil {
			rlog.Error("memory sample failed",
				"event", "control.memory_sample_failed",
				"frontend", string(kind),
				"err", err,
			)
		}
	}
}

func (s *Service) sampleFrontend(ctx context.Context, kind modelstate.FrontendKind) error {
	pids := s.liveFrontendPIDs(ctx, kind)
	if len(pids) == 0 {
		return setFrontendMemoryMB(ctx, kind, nil)
	}
	// Keep catalog pids in sync with the live worker (socket holder), not a wrapper.
	_ = refreshFrontendPIDsFromList(ctx, kind, pids)
	_ = s.syncModelPIDs(ctx, kind)

	mb, err := memoryMBFn(pids)
	if err != nil {
		return err
	}
	if err := setFrontendMemoryMB(ctx, kind, &mb); err != nil {
		return err
	}
	return insertMemorySample(ctx, kind, mb, memorySampleKeep)
}

func (s *Service) syncModelPIDs(ctx context.Context, kind modelstate.FrontendKind) error {
	rt := s.runtime(kind)
	if rt == nil || rt.Ready(ctx) != nil {
		return nil
	}
	rows, err := listModels(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Frontend != kind || row.Observed != modelstate.ModelLoaded {
			continue
		}
		pid, err := rt.ModelPID(ctx, row.NativeID)
		if err != nil || pid <= 0 {
			continue
		}
		p := int64(pid)
		if row.PID == nil || *row.PID != p {
			_ = setModelPID(ctx, row.ID, &p)
		}
	}
	return nil
}

func (s *Service) liveFrontendPIDs(ctx context.Context, kind modelstate.FrontendKind) []int64 {
	rt := s.runtime(kind)
	if rt != nil && rt.Ready(ctx) == nil {
		if live, err := rt.BackendPIDs(ctx); err == nil && len(live) > 0 {
			out := make([]int64, 0, len(live))
			for _, p := range live {
				if p > 1 {
					out = append(out, int64(p))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	row, err := getFrontend(ctx, kind)
	if err != nil {
		return nil
	}
	return row.PIDs
}

func refreshFrontendPIDsFromList(ctx context.Context, kind modelstate.FrontendKind, pids []int64) error {
	_, err := db.Exec(ctx, `
		UPDATE frontends
		SET pids = $2::bigint[], updated_at = NOW()
		WHERE kind = $1
	`, kind, formatInt64Array(pids))
	return err
}

func sumProcessMemoryMB(pids []int64) (int64, error) {
	if len(pids) == 0 {
		return 0, nil
	}
	if runtime.GOOS == "darwin" {
		return sumFootprintMB(pids)
	}
	return sumRSSMB(pids)
}

// sumFootprintMB uses macOS `footprint` phys_footprint (includes GPU/IOAccelerator).
func sumFootprintMB(pids []int64) (int64, error) {
	var total int64
	var lastErr error
	any := false
	for _, pid := range pids {
		mb, err := footprintMB(pid)
		if err != nil {
			lastErr = err
			continue
		}
		total += mb
		any = true
	}
	if !any {
		if lastErr != nil {
			return 0, lastErr
		}
		return 0, fmt.Errorf("footprint: no pids measured")
	}
	return total, nil
}

func footprintMB(pid int64) (int64, error) {
	out, err := exec.Command("footprint", strconv.FormatInt(pid, 10)).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("footprint %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	text := string(out)
	if mb, ok := parseFootprintMB(text); ok {
		return mb, nil
	}
	return 0, fmt.Errorf("footprint %d: could not parse output", pid)
}

func parseFootprintMB(text string) (int64, bool) {
	if m := footprintHeaderRe.FindStringSubmatch(text); len(m) == 3 {
		return toWholeMB(m[1], m[2])
	}
	if m := physFootprintRe.FindStringSubmatch(text); len(m) == 3 {
		return toWholeMB(m[1], m[2])
	}
	return 0, false
}

func toWholeMB(num, unit string) (int64, bool) {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToUpper(unit) {
	case "KB":
		return int64(f / 1024), true
	case "MB":
		return int64(f), true
	case "GB":
		return int64(f * 1024), true
	default:
		return 0, false
	}
}

func sumRSSMB(pids []int64) (int64, error) {
	if len(pids) == 0 {
		return 0, nil
	}
	args := []string{"-o", "rss=", "-p", joinPIDs(pids)}
	out, err := exec.Command("ps", args...).CombinedOutput()
	if err != nil {
		var totalKB int64
		any := false
		for _, p := range pids {
			o, e := exec.Command("ps", "-o", "rss=", "-p", strconv.FormatInt(p, 10)).CombinedOutput()
			if e != nil {
				continue
			}
			kb, ok := parseRSSKB(string(o))
			if !ok {
				continue
			}
			totalKB += kb
			any = true
		}
		if !any {
			return 0, err
		}
		return totalKB / 1024, nil
	}
	kb, ok := parseRSSKB(string(out))
	if !ok {
		return 0, nil
	}
	return kb / 1024, nil
}

func parseRSSKB(raw string) (int64, bool) {
	var total int64
	any := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		total += n
		any = true
	}
	return total, any
}

func joinPIDs(pids []int64) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.FormatInt(p, 10)
	}
	return strings.Join(parts, ",")
}

func formatInt64Array(pids []int64) string {
	if len(pids) == 0 {
		return "{}"
	}
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.FormatInt(p, 10)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
