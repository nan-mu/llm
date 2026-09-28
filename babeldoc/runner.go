package babeldoc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner produces a bilingual PDF from a source PDF path.
type Runner interface {
	// Run writes dual PDF under outDir and returns its absolute path.
	Run(ctx context.Context, babelDOCRoot, configFile, sourcePath, outDir string) (dualPath string, err error)
}

type pixiRunner struct{}

func newPixiRunner() Runner { return &pixiRunner{} }

func (pixiRunner) Run(ctx context.Context, babelDOCRoot, configFile, sourcePath, outDir string) (string, error) {
	cfg := filepath.Join(babelDOCRoot, configFile)
	if _, err := os.Stat(cfg); err != nil {
		return "", fmt.Errorf("babeldoc config: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "pixi", "run", "babeldoc",
		"-c", cfg,
		"--files", sourcePath,
		"-o", outDir,
		"--no-mono",
	)
	cmd.Dir = babelDOCRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return "", fmt.Errorf("pixi babeldoc: %w: %s", err, msg)
	}

	dual, err := findDualPDF(outDir)
	if err != nil {
		return "", err
	}
	return dual, nil
}

func findDualPDF(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".dual.pdf") {
			matches = append(matches, filepath.Join(dir, name))
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no *.dual.pdf in %s", dir)
	}
	if len(matches) > 1 {
		// Prefer non-debug / standard naming; still deterministic: first sorted.
		return matches[0], nil
	}
	return matches[0], nil
}

// StubRunner writes a minimal dual PDF for tests.
type StubRunner struct {
	DualPDF []byte
	Err     error
}

func (r *StubRunner) Run(ctx context.Context, _, _, _, outDir string) (string, error) {
	if r.Err != nil {
		return "", r.Err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "stub.zh.dual.pdf")
	body := r.DualPDF
	if len(body) == 0 {
		body = []byte("%PDF-1.4 stub dual\n%%EOF\n")
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
