package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	t0   = "2026-11-19T15:00:00"
	late = "2026-11-20T00:46:00"
)

var ggBin string

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "gg-build")
	ggBin = filepath.Join(dir, "gg")
	if out, err := exec.Command("go", "build", "-o", ggBin, ".").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t         *testing.T
	tmp, home string
	repo      string
}

func newEnv(t *testing.T) *env {
	tmp, _ := filepath.EvalSymlinks(t.TempDir())
	e := &env{t: t, tmp: tmp, home: filepath.Join(tmp, "home"), repo: filepath.Join(tmp, "repo")}
	os.Mkdir(e.repo, 0o755)
	e.git(e.repo, "init", "-q")
	e.git(e.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "i")
	return e
}

func (e *env) git(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %s", args, out)
	}
}

// run executes gg in cwd with GG_NOW=now and returns stdout and the exit code.
func (e *env) run(cwd, now, stdin string, args ...string) (string, int) {
	cmd := exec.Command(ggBin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GG_HOME="+e.home, "GG_NOW="+now)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return string(out), code
}

func (e *env) gg(now string, args ...string) (string, int) { return e.run(e.repo, now, "", args...) }

func (e *env) hook(event, now string, extra map[string]any) (string, int) {
	m := map[string]any{"hook_event_name": event, "cwd": e.repo}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return e.run(e.repo, now, string(b), "hook")
}

func (e *env) seed() {
	for _, a := range [][3]string{{"900", "English n-gram", "4h"}, {"890", "Quanpin 8M", "4h"}, {"896", "Wanxiang lexicon", "1h"}} {
		if _, code := e.gg(t0, "add", a[0], a[1], "--why", "w "+a[0], "--budget", a[2]); code != 0 {
			e.t.Fatalf("add %s failed", a[0])
		}
	}
	e.gg(t0, "rank", "900", "890", "896")
	if _, code := e.gg(t0, "start", "896"); code != 0 {
		e.t.Fatal("start 896 failed")
	}
}

func has(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
}

func empty(t *testing.T, out string) {
	t.Helper()
	if out != "" {
		t.Errorf("want empty output, got %q", out)
	}
}

func TestReplay(t *testing.T) {
	e := newEnv(t)
	e.seed()
	out, _ := e.hook("UserPromptSubmit", late, nil)
	has(t, out, "OVER BUDGET", "9h46m", "WARNING: #900")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "Rules:") {
		t.Errorf("last line is not Rules: %q", lines[len(lines)-1])
	}
}

func TestStopBlocks(t *testing.T) {
	e := newEnv(t)
	e.seed()
	out, _ := e.hook("Stop", late, nil)
	var o map[string]string
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("bad JSON %q: %v", out, err)
	}
	if o["decision"] != "block" {
		t.Errorf("decision = %q", o["decision"])
	}
	has(t, o["reason"], "896", "900")
	out, _ = e.hook("Stop", late, map[string]any{"stop_hook_active": true})
	empty(t, out)
}

func TestStartRefused(t *testing.T) {
	e := newEnv(t)
	e.seed()
	files, _ := filepath.Glob(filepath.Join(e.home, "*"))
	before, _ := os.ReadFile(files[0])
	if _, code := e.gg(t0, "start", "900"); code == 0 {
		t.Error("start 900 exited 0")
	}
	after, _ := os.ReadFile(files[0])
	if string(before) != string(after) {
		t.Error("ledger changed")
	}
}

func TestPark(t *testing.T) {
	e := newEnv(t)
	e.seed()
	if _, code := e.gg(late, "park", "896", "stuck"); code != 0 {
		t.Fatal("park failed")
	}
	out, _ := e.gg(late, "status")
	has(t, out, "Nothing active. Next by rank: #900.")
	out, _ = e.hook("Stop", late, nil)
	empty(t, out)
}

func TestNoLedger(t *testing.T) {
	e := newEnv(t)
	for _, ev := range []string{"UserPromptSubmit", "Stop"} {
		out, code := e.hook(ev, t0, nil)
		if code != 0 {
			t.Errorf("%s exit %d", ev, code)
		}
		empty(t, out)
	}
	out, code := e.hook("SessionStart", t0, nil)
	if code != 0 {
		t.Errorf("SessionStart exit %d", code)
	}
	has(t, out, "gg add", "the user never runs gg")
	out, _ = e.gg(t0, "status")
	empty(t, out)
}

func TestAccumulates(t *testing.T) {
	e := newEnv(t)
	e.gg(t0, "add", "1", "x", "--why", "w", "--budget", "3h")
	e.gg("2026-11-19T10:00:00", "start", "1")
	e.gg("2026-11-19T10:30:00", "park", "1", "n")
	e.gg("2026-11-19T12:00:00", "start", "1")
	out, _ := e.gg("2026-11-19T12:20:00", "status")
	has(t, out, "50m spent of 3h")
}

func TestWorktree(t *testing.T) {
	e := newEnv(t)
	e.seed()
	wt := filepath.Join(e.tmp, "wt")
	e.git(e.repo, "worktree", "add", "-q", wt, "-b", "b")
	a, _ := e.run(wt, t0, "", "status")
	b, _ := e.gg(t0, "status")
	if a != b {
		t.Errorf("worktree status differs:\n%s\n---\n%s", a, b)
	}
	has(t, a, "#896")
}

func TestSessionStartActive(t *testing.T) {
	e := newEnv(t)
	e.seed()
	out, _ := e.hook("SessionStart", "2026-11-19T15:30:00", nil)
	has(t, out, "the user never runs gg", "ACTIVE #896")
}

func TestAllDone(t *testing.T) {
	e := newEnv(t)
	e.seed()
	for _, id := range []string{"896", "900", "890"} {
		e.gg(t0, "done", id)
	}
	out, _ := e.hook("UserPromptSubmit", t0, nil)
	empty(t, out)
	out, _ = e.hook("SessionStart", t0, nil)
	has(t, out, "the user never runs gg")
	if strings.Contains(out, "[greater-good] 20") {
		t.Errorf("status printed:\n%s", out)
	}
	out, _ = e.gg(t0, "status")
	empty(t, out)
}

func TestLine(t *testing.T) {
	e := newEnv(t)
	e.seed()
	out, _ := e.gg(late, "line")
	has(t, out, "3▶ #896", "9h46m/1h OVER", "\033[1;31m")
	if strings.Index(out, "1 #900") >= strings.Index(out, "2 #890") {
		t.Errorf("rank order wrong: %q", out)
	}
}

func TestLineDir(t *testing.T) {
	e := newEnv(t)
	other := filepath.Join(e.tmp, "other")
	os.Mkdir(other, 0o755)
	out, _ := e.run(other, t0, "", "line", e.repo)
	empty(t, out)
	e.seed()
	out, _ = e.run(other, t0, "", "line", e.repo)
	has(t, out, "#896")
	for _, id := range []string{"896", "900", "890"} {
		e.gg(t0, "done", id)
	}
	out, _ = e.run(other, t0, "", "line", e.repo)
	empty(t, out)
}

func TestMissingCwd(t *testing.T) {
	e := newEnv(t)
	out, code := e.run(e.repo, t0, `{"hook_event_name":"SessionStart","cwd":"/nonexistent/x"}`, "hook")
	if code != 0 {
		t.Errorf("exit %d", code)
	}
	has(t, out, "the user never runs gg")
}

func TestUnderBudget(t *testing.T) {
	e := newEnv(t)
	e.seed()
	now := "2026-11-19T15:30:00"
	out, _ := e.hook("Stop", now, nil)
	empty(t, out)
	out, _ = e.gg(now, "status")
	if strings.Contains(out, "OVER BUDGET") {
		t.Errorf("unexpected OVER BUDGET:\n%s", out)
	}
}
