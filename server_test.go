package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points TIX_HOME at a temporary folder, so no test can reach ~/tasks (R5).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "tix-web-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TIX_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func fakeTix(t *testing.T) (tixCLI, string) {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-tix.sh")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("FAKE_TIX_LOG", log)
	return tixCLI{bin: bin}, log
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBoard(t *testing.T) {
	tix, log := fakeTix(t)
	rec := get(t, newServer(tix, "8737"), "127.0.0.1:8737", "/api/board")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Statuses []statusRow                  `json:"statuses"`
		Projects []projectRow                 `json:"projects"`
		Tasks    []map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Statuses) != 2 || got.Statuses[1] != (statusRow{Name: "open", Kind: "open", Count: 1}) {
		t.Errorf("statuses %+v", got.Statuses)
	}
	if len(got.Projects) != 2 || got.Projects[0] != (projectRow{Name: "shop", Count: 1}) || got.Projects[1].Count != 0 {
		t.Errorf("projects %+v", got.Projects)
	}
	if len(got.Tasks) != 1 || string(got.Tasks[0]["id"]) != `"T-1"` {
		t.Fatalf("tasks %s", rec.Body)
	}
	if _, ok := got.Tasks[0]["path"]; ok {
		t.Error("task path leaked to the browser")
	}
	args, _ := os.ReadFile(log)
	if want := "status\n--\nprojects\n--\nlist\n--all\n--json\n--project\n\n--\n"; string(args) != want {
		t.Errorf("tix args\n%q\nwant\n%q", args, want)
	}
}

// spec: R3
func TestTaskHTMLEscapesRawMarkup(t *testing.T) {
	tix, _ := fakeTix(t)
	rec := get(t, newServer(tix, "8737"), "localhost:8737", "/api/tasks/T-7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		ID       string           `json:"id"`
		Body     string           `json:"body"`
		BodyHTML string           `json:"body_html"`
		Notes    []map[string]any `json:"notes"`
		Path     *string          `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "T-7" || !strings.Contains(got.Body, "<script>") || got.Path != nil {
		t.Errorf("task fields %+v", got)
	}
	if !strings.Contains(got.BodyHTML, "<h2>Abstract</h2>") {
		t.Errorf("body not rendered: %s", got.BodyHTML)
	}
	note, _ := got.Notes[0]["html"].(string)
	for _, html := range []string{got.BodyHTML, note} {
		for _, bad := range []string{"<script", "<img", "javascript:"} {
			if strings.Contains(html, bad) {
				t.Errorf("raw %s reached the page: %s", bad, html)
			}
		}
	}
	if !strings.Contains(note, "<strong>done</strong>") {
		t.Errorf("note not rendered: %s", note)
	}
}

func TestTaskErrorsPassTixText(t *testing.T) {
	tix, _ := fakeTix(t)
	h := newServer(tix, "8737")
	for _, tc := range []struct {
		id   string
		code int
		exit int
		msg  string
	}{
		{"T-404", http.StatusNotFound, 4, "err T-404: task not found"},
		{"T-409", http.StatusConflict, 5, "err T-409: rule says no"},
		{"T-410", http.StatusConflict, 3, "err T-410: changed since rev abc"},
		{"T-500", http.StatusInternalServerError, 1, "boom"},
		{"--as", http.StatusBadRequest, 6, "err --as: not a task id, want T-N"},
	} {
		rec := get(t, h, "127.0.0.1:8737", "/api/tasks/"+tc.id)
		var got struct {
			Error string `json:"error"`
			Code  int    `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v: %s", tc.id, err, rec.Body)
		}
		if rec.Code != tc.code || got.Code != tc.exit || got.Error != tc.msg {
			t.Errorf("%s: got %d %+v, want %d %d %q", tc.id, rec.Code, got, tc.code, tc.exit, tc.msg)
		}
	}
}

// spec: R2
func TestForeignHostRefused(t *testing.T) {
	tix, _ := fakeTix(t)
	h := newServer(tix, "8737")
	for host, want := range map[string]int{
		"127.0.0.1:8737":    http.StatusOK,
		"localhost:8737":    http.StatusOK,
		"evil.example:8737": http.StatusForbidden,
		"127.0.0.1:9999":    http.StatusForbidden,
		"localhost":         http.StatusForbidden,
	} {
		for _, path := range []string{"/", "/api/board"} {
			if rec := get(t, h, host, path); rec.Code != want {
				t.Errorf("%s%s: %d, want %d", host, path, rec.Code, want)
			}
		}
	}
}

func TestPageIsEmbedded(t *testing.T) {
	tix, _ := fakeTix(t)
	rec := get(t, newServer(tix, "8737"), "127.0.0.1:8737", "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<script src="app.js">`) {
		t.Errorf("index: %d %s", rec.Code, rec.Body)
	}
}

// spec: R4
func TestArgsReachTixAsArray(t *testing.T) {
	tix, log := fakeTix(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	arg := "x; touch " + marker + " $(touch " + marker + ")"
	if _, err := tix.run(t.Context(), "list", arg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran as a shell command")
	}
	args, _ := os.ReadFile(log)
	if want := "list\n" + arg + "\n--\n"; string(args) != want {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestRunRefuses(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := run([]string{"-tix", "tix"}); err == nil || !strings.Contains(err.Error(), "tix not found") {
		t.Errorf("no tix: %v", err)
	}
	sh, _ := filepath.Abs("testdata/fake-tix.sh")
	if err := run([]string{"-tix", sh, "-addr", "0.0.0.0:0"}); err == nil || !strings.Contains(err.Error(), "only 127.0.0.1") {
		t.Errorf("public addr: %v", err)
	}
}

func TestParseRefusesUnknownFormat(t *testing.T) {
	if _, err := parseStatuses([]byte("idea open\n")); err == nil {
		t.Error("status with two columns parsed")
	}
	if _, err := parseProjects([]byte("shop /home/u/shop\n")); err == nil {
		t.Error("project without count parsed")
	}
}
