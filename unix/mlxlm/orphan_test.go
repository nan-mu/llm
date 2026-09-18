package mlxlm

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"encore.app/unix/internal/proc"
)

func TestRealWorkerStopClearsSockAndPID(t *testing.T) {
	bin := realWorkerBin(t)
	cwd, err := os.MkdirTemp("/tmp", "mw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })

	rt, err := NewWithConfig(Config{
		Bin:       bin,
		Cwd:       cwd,
		ModelsDir: t.TempDir(),
		APIKey:    "k",
		ExtraEnv:  []string{"MLXLM_SKIP_LOAD=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	if err := rt.EnsureLoaded(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(cwd, "m1.sock")
	pidPath := sock + ".pid"
	if _, err := os.Stat(sock); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("sock should be removed")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("worker pid %d still alive after Stop", pid)
}

func TestRealWorkerUnloadLeavesSibling(t *testing.T) {
	bin := realWorkerBin(t)
	cwd, err := os.MkdirTemp("/tmp", "mw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })

	rt, err := NewWithConfig(Config{
		Bin:       bin,
		Cwd:       cwd,
		ModelsDir: t.TempDir(),
		APIKey:    "k",
		ExtraEnv:  []string{"MLXLM_SKIP_LOAD=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	if err := rt.EnsureLoaded(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := rt.EnsureLoaded(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := rt.Unload(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "a.sock")); !os.IsNotExist(err) {
		t.Fatal("a.sock should be gone")
	}
	sockB := filepath.Join(cwd, "b.sock")
	if _, err := os.Stat(sockB); err != nil {
		t.Fatal(err)
	}
	if err := healthUnix(sockB); err != nil {
		t.Fatalf("b health: %v", err)
	}
}

func TestRealWorkerExitsWhenParentDies(t *testing.T) {
	bin := realWorkerBin(t)
	cwd, err := os.MkdirTemp("/tmp", "mw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	sock := filepath.Join(cwd, "orphan.sock")

	// Stand-in "supervisor" process whose PID we pass to the worker.
	standIn := exec.Command("sleep", "60")
	if err := standIn.Start(); err != nil {
		t.Fatal(err)
	}
	standInPID := standIn.Process.Pid
	t.Cleanup(func() {
		_ = standIn.Process.Kill()
		_, _ = standIn.Process.Wait()
	})

	worker := exec.Command(bin,
		"--model-path", cwd,
		"--host", sock,
		"--api-key", "k",
		"--parent-pid", strconv.Itoa(standInPID),
		"--skip-load",
	)
	worker.Env = append(os.Environ(), "MLXLM_SKIP_LOAD=1")
	worker.Dir = cwd
	worker.Stdout = os.Stderr
	worker.Stderr = os.Stderr
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = worker.Process.Kill()
		_, _ = worker.Process.Wait()
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := healthUnix(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := healthUnix(sock); err != nil {
		t.Fatalf("worker never healthy: %v", err)
	}

	_ = standIn.Process.Kill()
	_, _ = standIn.Process.Wait()

	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err := healthUnix(sock); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("worker still serving /health after supervised parent pid exited")
}

func realWorkerBin(t *testing.T) string {
	t.Helper()
	root, err := proc.AppRoot()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "unix", "mlx_lm", "bin", "mlx_lm_server")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("mlx_lm_server wrapper missing")
	}
	if _, err := exec.LookPath("pixi"); err != nil {
		t.Skip("pixi not installed")
	}
	return bin
}

func healthUnix(sock string) error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		Timeout: 2 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, "http://localhost/health", nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return os.ErrInvalid
	}
	return nil
}
