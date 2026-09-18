package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	_ = os.WriteFile(p.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)

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

// Stop sends SIGTERM to the frontend (and any instance children), then SIGKILL if ctx ends first.
func (p *Proc) Stop(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	pids := p.frontendPIDs(cmd)
	p.signalPIDs(pids, syscall.SIGTERM)

	wait := done
	if wait == nil {
		wait = make(chan struct{})
		close(wait)
	}

	select {
	case <-wait:
		p.waitPIDs(ctx, pids)
	case <-ctx.Done():
		p.signalPIDs(pids, syscall.SIGKILL)
		if done != nil {
			<-done
		}
		p.waitPIDs(context.Background(), pids)
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

func (p *Proc) pidPath() string {
	if p.cfg.SocketPath == "" {
		return ""
	}
	return p.cfg.SocketPath + ".pid"
}

func (p *Proc) frontendPIDs(cmd *exec.Cmd) []int {
	seen := map[int]struct{}{}
	add := func(pid int) {
		if pid > 1 && pid != os.Getpid() {
			seen[pid] = struct{}{}
		}
	}
	if cmd != nil && cmd.Process != nil {
		add(cmd.Process.Pid)
	}
	for _, pid := range readPIDFile(p.pidPath()) {
		add(pid)
	}
	for _, pid := range pidsHoldingSocket(p.cfg.SocketPath, p.cfg.Kind) {
		add(pid)
	}
	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out
}

func (p *Proc) signalPIDs(pids []int, sig syscall.Signal) {
	// Prefer process-group kill so Python/mlx workers and their children die together.
	seenPG := map[int]struct{}{}
	for _, pid := range pids {
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 1 {
			if _, ok := seenPG[pgid]; !ok {
				seenPG[pgid] = struct{}{}
				_ = syscall.Kill(-pgid, sig)
			}
		}
		_ = syscall.Kill(pid, sig)
	}
}

func (p *Proc) waitPIDs(ctx context.Context, pids []int) {
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if err := syscall.Kill(pid, 0); err == nil {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func readPIDFile(path string) []int {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return nil
	}
	return []int{pid}
}

func pidsHoldingSocket(socketPath, kind string) []int {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil
	}
	abs, err := filepath.Abs(socketPath)
	if err != nil {
		abs = socketPath
	}
	base := filepath.Base(abs)
	dir := filepath.Dir(abs)
	cmd := exec.Command("lsof", "-nP", "-U")
	out, _ := cmd.Output()
	if len(out) == 0 {
		return nil
	}
	kind = strings.ToLower(kind)
	seen := map[int]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, base) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 1 {
			continue
		}
		cwd := processCWD(pid)
		if cwd != "" && cwd != dir {
			name := strings.ToLower(fields[0])
			if !strings.Contains(name, kind) &&
				!strings.Contains(name, "llama") &&
				!strings.Contains(name, "mlxcel") &&
				!strings.Contains(name, "mlxlm") &&
				!strings.Contains(name, "fakemlx") &&
				!strings.Contains(name, "python") {
				continue
			}
		}
		seen[pid] = struct{}{}
	}
	pids := make([]int, 0, len(seen))
	for pid := range seen {
		pids = append(pids, pid)
	}
	return pids
}

func processCWD(pid int) string {
	out, err := exec.Command("lsof", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimSpace(strings.TrimPrefix(line, "n"))
		}
	}
	return ""
}

func (p *Proc) removeSocket() {
	if p.cfg.SocketPath != "" {
		_ = os.Remove(p.cfg.SocketPath)
		_ = os.Remove(p.pidPath())
	}
}

// Done returns a channel closed when the child exits. Nil if not started.
func (p *Proc) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// Alive reports whether the child is still running.
func (p *Proc) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aliveLocked()
}

// PID returns the worker process id, or 0 if not running.
// When a Unix socket is configured, prefer the PID holding that socket
// (e.g. python under a pixi/wrapper parent) over the direct child PID.
func (p *Proc) PID() int {
	p.mu.Lock()
	alive := p.aliveLocked()
	direct := 0
	if alive && p.cmd != nil && p.cmd.Process != nil {
		direct = p.cmd.Process.Pid
	}
	sock := p.cfg.SocketPath
	kind := p.cfg.Kind
	p.mu.Unlock()
	if !alive {
		return 0
	}
	if sock != "" {
		holders := pidsHoldingSocket(sock, kind)
		for _, h := range holders {
			if h > 1 && h != direct {
				return h
			}
		}
		if len(holders) > 0 && holders[0] > 1 {
			return holders[0]
		}
	}
	return direct
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
