package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const tixVersion = "0.9.1"

// realTix needs tix 0.9.1 in PATH and fails without it: a skipped e2e would hide a broken contract.
func realTix(t *testing.T) tixCLI {
	t.Helper()
	path, err := exec.LookPath("tix")
	if err != nil {
		t.Fatalf("e2e needs tix %s in PATH: %v", tixVersion, err)
	}
	out, err := exec.CommandContext(t.Context(), path, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != tixVersion {
		t.Fatalf("e2e needs tix %s, got %q (%v)", tixVersion, out, err)
	}
	return tixCLI{bin: path}
}

// snapshot lists every file under dir with its content hash.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%s %x", p, sha256.Sum256(b)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// spec: R1
// spec: R5
func TestE2ERealTixReadOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e runs real tix; the fast tier skips it")
	}
	tix := realTix(t)
	home := t.TempDir()
	t.Setenv("TIX_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	seed := exec.CommandContext(t.Context(), tix.bin, "new", "e2e task", "--project", "demo", "--body-file", "-", "--as", "human")
	seed.Stdin = strings.NewReader("## Abstract\n\nhi <script>alert(1)</script>\n\n## Details\n\n- [ ] one\n")
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	before := snapshot(t, home)

	h := newServer(tix, "8737")
	rec := get(t, h, "127.0.0.1:8737", "/api/board")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"e2e task"`) {
		t.Fatalf("board %d: %s", rec.Code, rec.Body)
	}
	rec = get(t, h, "127.0.0.1:8737", "/api/tasks/T-1")
	var task struct {
		BodyHTML string `json:"body_html"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("task %d: %v: %s", rec.Code, err, rec.Body)
	}
	if strings.Contains(task.BodyHTML, "<script") || !strings.Contains(task.BodyHTML, "<h2>Abstract</h2>") {
		t.Errorf("body_html %s", task.BodyHTML)
	}
	if after := snapshot(t, home); after != before {
		t.Errorf("TIX_HOME changed by reads\nbefore\n%s\nafter\n%s", before, after)
	}

	// After the snapshot: tix itself logs a failed command to .tix/log.
	if rec = get(t, h, "127.0.0.1:8737", "/api/tasks/T-99"); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: %d %s", rec.Code, rec.Body)
	}
}
