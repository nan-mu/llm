package supervisor_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/internal/fakemlx"
	"encore.app/unix/internal/supervisor"
)

func TestStartWithoutLoadHasNoChildren(t *testing.T) {
	sup := supervisor.New("mlxlm")
	ctx := context.Background()
	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Stop(ctx) })

	if err := sup.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := sup.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %v, want empty", list)
	}
}

func TestLoadTwoUnloadOne(t *testing.T) {
	bin, err := fakemlx.Build()
	if err != nil {
		t.Fatal(err)
	}
	cwd := shortTemp(t)
	sup := supervisor.New("mlxlm")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Stop(context.Background()) })

	sockA := filepath.Join(cwd, "a.sock")
	sockB := filepath.Join(cwd, "b.sock")
	if err := sup.Load(ctx, "a", spawn(bin, cwd, sockA, "/models/a")); err != nil {
		t.Fatal(err)
	}
	if err := sup.Load(ctx, "b", spawn(bin, cwd, sockB, "/models/b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sockA); err != nil {
		t.Fatalf("sock a: %v", err)
	}
	if _, err := os.Stat(sockB); err != nil {
		t.Fatalf("sock b: %v", err)
	}

	list, err := sup.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d", len(list))
	}

	if err := sup.Unload(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sockA); !os.IsNotExist(err) {
		t.Fatalf("sock a should be gone, err=%v", err)
	}
	if _, err := os.Stat(sockB); err != nil {
		t.Fatalf("sock b should remain: %v", err)
	}
	got, err := sup.Get(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != modelstate.ModelLoaded {
		t.Fatalf("b state = %s", got.State)
	}
	if _, err := sup.Get(ctx, "a"); !unix.Is(err, unix.CodeNotFound) {
		t.Fatalf("get a after unload: %v", err)
	}
}

func TestDeadChildMarkedFailed(t *testing.T) {
	bin, err := fakemlx.Build()
	if err != nil {
		t.Fatal(err)
	}
	cwd := shortTemp(t)
	sup := supervisor.New("mlxlm")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Stop(context.Background()) })

	sock := filepath.Join(cwd, "x.sock")
	if err := sup.Load(ctx, "x", spawn(bin, cwd, sock, "/models/x")); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(sock + ".pid")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("pid file %q: %v", raw, err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	_ = proc.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := sup.Get(ctx, "x")
		if err != nil {
			t.Fatal(err)
		}
		if got.State == modelstate.ModelFailed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("expected failed state after kill")
}

func TestIdempotentLoad(t *testing.T) {
	bin, err := fakemlx.Build()
	if err != nil {
		t.Fatal(err)
	}
	cwd := shortTemp(t)
	sup := supervisor.New("mlxlm")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Stop(context.Background()) })

	sock := filepath.Join(cwd, "idem.sock")
	spec := spawn(bin, cwd, sock, "/models/idem")
	if err := sup.Load(ctx, "idem", spec); err != nil {
		t.Fatal(err)
	}
	if err := sup.Load(ctx, "idem", spec); err != nil {
		t.Fatal(err)
	}
	list, err := sup.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	pid, ok := sup.PID("idem")
	if !ok || pid <= 0 {
		t.Fatalf("PID = %d ok=%v", pid, ok)
	}
	if got := sup.PIDs(); len(got) != 1 || got[0] != pid {
		t.Fatalf("PIDs = %v", got)
	}
}

func spawn(bin, cwd, sock, path string) supervisor.SpawnSpec {
	return supervisor.SpawnSpec{
		Bin:        bin,
		Args:       []string{"--host", sock, "--model-path", path, "--api-key", "k"},
		Cwd:        cwd,
		SocketPath: sock,
		APIKey:     "k",
		Path:       path,
	}
}

func shortTemp(t *testing.T) string {
	t.Helper()
	// macOS AF_UNIX paths cap at 104 bytes; t.TempDir() plus the test name overflows.
	dir, err := os.MkdirTemp("", "su-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
