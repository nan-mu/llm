package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"encore.app/unix/internal/logx"
)

const socketWaitPoll = 50 * time.Millisecond

// Config describes a frontend child process.
type Config struct {
	Kind       string
	Bin        string
	Args       []string
	Cwd        string
	SocketPath string
	ExtraEnv   []string
}

// Proc is one os/exec frontend process.
type Proc struct {
	cfg     Config
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

// New returns a process handle that has not started yet.
func New(cfg Config) *Proc {
	return &Proc{cfg: cfg}
}

// AppRoot walks from the working directory until it finds encore.app.
func AppRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "encore.app")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("encore.app not found from %s", dir)
		}
		dir = parent
	}
}

// Start launches the process. It is not idempotent at this layer; callers check Ready first.
func (p *Proc) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.aliveLocked() {
		return nil
	}

	if err := os.MkdirAll(p.cfg.Cwd, 0o755); err != nil {
		return err
	}
	if p.cfg.SocketPath != "" {
		_ = os.Remove(p.cfg.SocketPath)
	}

	bin, err := resolveBin(p.cfg.Bin)
	if err != nil {
		return err
	}

	cmd := exec.Command(bin, p.cfg.Args...)
	cmd.Dir = p.cfg.Cwd
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), p.cfg.ExtraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	p.cmd = cmd
	p.done = done
	p.waitErr = nil
	go func() {
		p.waitErr = cmd.Wait()
		close(done)
	}()

	logx.Info("frontend process started",
		"event", "frontend.started",
		"kind", p.cfg.Kind,
		"bin", bin,
		"cwd", p.cfg.Cwd,
		"socket", p.cfg.SocketPath,
		"pid", cmd.Process.Pid,
	)

	if p.cfg.SocketPath == "" {
		return nil
	}
	if err := waitForSocket(ctx, p.cfg.SocketPath, p.aliveLocked); err != nil {
		p.killLocked()
		return err
	}
	return nil
}

func (p *Proc) killLocked() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = p.cmd.Process.Kill()
	}
	if p.done != nil {
		<-p.done
	}
	p.cmd = nil
	p.done = nil
	p.removeSocket()
}

// Stop sends SIGTERM to the process group, then SIGKILL if ctx ends first.
func (p *Proc) Stop(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		p.removeSocket()
		return nil
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	} else {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}

	if done == nil {
		p.removeSocket()
		return nil
	}

	select {
	case <-done:
	case <-ctx.Done():
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
		<-done
	}

	p.mu.Lock()
	p.cmd = nil
	p.done = nil
	p.mu.Unlock()
	p.removeSocket()

	logx.Info("frontend process stopped",
		"event", "frontend.stopped",
		"kind", p.cfg.Kind,
		"socket", p.cfg.SocketPath,
	)
	return nil
}

// Alive reports whether the child is still running.
func (p *Proc) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aliveLocked()
}

func (p *Proc) aliveLocked() bool {
	if p.cmd == nil || p.cmd.Process == nil || p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *Proc) removeSocket() {
	if p.cfg.SocketPath != "" {
		_ = os.Remove(p.cfg.SocketPath)
	}
}

func resolveBin(bin string) (string, error) {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return "", fmt.Errorf("empty binary path")
	}
	if filepath.IsAbs(bin) || strings.ContainsRune(bin, os.PathSeparator) {
		return filepath.Abs(bin)
	}
	return exec.LookPath(bin)
}

func waitForSocket(ctx context.Context, path string, alive func() bool) error {
	ticker := time.NewTicker(socketWaitPoll)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if alive != nil && !alive() {
			return fmt.Errorf("frontend process exited before socket %s appeared", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
