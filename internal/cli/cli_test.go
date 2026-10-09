package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err := Run(args, &out, &errBuf)
	return out.String(), err
}

func TestVersionAndHelp(t *testing.T) {
	out, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, progName) {
		t.Fatalf("version output = %q", out)
	}
	if _, err := run(t, "help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	if _, err := run(t, "bogus"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestNewAddListLifecycle(t *testing.T) {
	root := t.TempDir()

	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	project := filepath.Join(root, "demo")
	if _, err := os.Stat(filepath.Join(project, "main.go")); err != nil {
		t.Fatalf("new did not write main.go: %v", err)
	}

	listOut, err := run(t, "list", "--dir", project)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "Installed in demo") {
		t.Fatalf("list output = %q", listOut)
	}

	if _, err := run(t, "add", "web", "--dir", project); err != nil {
		t.Fatalf("add web: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "web", "package.json")); err != nil {
		t.Fatalf("add web did not write the frontend: %v", err)
	}
	// web requires http, which the same command installs without being asked.
	if _, err := os.Stat(filepath.Join(project, "internal", "httpserver", "http.go")); err != nil {
		t.Fatalf("add web did not resolve the http dependency: %v", err)
	}

	// Repeating add is a no-op that still succeeds.
	out, err := run(t, "add", "web", "--dir", project)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add output = %q", out)
	}
}

func TestAddHTTPThenWebInstallsEachOnce(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	project := filepath.Join(root, "demo")

	if _, err := run(t, "add", "http", "--dir", project); err != nil {
		t.Fatalf("add http: %v", err)
	}
	out, err := run(t, "add", "web", "--dir", project)
	if err != nil {
		t.Fatalf("add web: %v", err)
	}
	if strings.Contains(out, "required capability") {
		t.Fatalf("web reinstalled an already-present dependency: %q", out)
	}

	listOut, err := run(t, "list", "--dir", project)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, capability := range []string{"http", "web"} {
		if !strings.Contains(listOut, "* "+capability) {
			t.Errorf("%s not marked installed in list output: %q", capability, listOut)
		}
	}
}

func TestAddDBInstallsWithoutHTTP(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "db", "--dir", dir)
	if err != nil {
		t.Fatalf("add db: %v", err)
	}
	if !strings.Contains(out, "internal/data/data.go") {
		t.Fatalf("add db output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "data", "data.go")); err != nil {
		t.Fatalf("add db did not write the repository: %v", err)
	}
	// db is independent of the HTTP capabilities.
	for _, path := range []string{"internal/httpserver", "internal/api", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add db created %s", path)
		}
	}

	// Repeating add is a no-op that still succeeds.
	out, err = run(t, "add", "db", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add output = %q", out)
	}

	listOut, err := run(t, "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "* db") {
		t.Errorf("db not marked installed in list output: %q", listOut)
	}
}

func TestAddRedisInstallsConfigOnly(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "redis", "--dir", dir)
	if err != nil {
		t.Fatalf("add redis: %v", err)
	}
	if !strings.Contains(out, "internal/redisclient/redisclient.go") {
		t.Fatalf("add redis output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "redisclient", "redisclient.go")); err != nil {
		t.Fatalf("add redis did not write the client: %v", err)
	}
	// redis is independent of the HTTP and database capabilities.
	for _, path := range []string{"internal/httpserver", "internal/api", "internal/data", "internal/di", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add redis created %s", path)
		}
	}

	// Repeating add is a no-op that still succeeds.
	out, err = run(t, "add", "redis", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add output = %q", out)
	}

	listOut, err := run(t, "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "* redis") {
		t.Errorf("redis not marked installed in list output: %q", listOut)
	}
}

func TestNewDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	out, err := run(t, "new", "demo", "--dir", root, "--dry-run")
	if err != nil {
		t.Fatalf("new --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatal("dry run created the project")
	}
}

func TestHelpMentionsLoomCapability(t *testing.T) {
	out, err := run(t, "help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"loom", "cron", "mail", "storage"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help does not mention the %s capability:\n%s", want, out)
		}
	}
}

func skillsPath(dir string) string {
	return filepath.Join(dir, ".agents", "skills", "weld", "SKILL.md")
}

func TestSkillsFreshGenerationAndRepeat(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "http", "--dir", dir); err != nil {
		t.Fatalf("add http: %v", err)
	}
	if _, err := run(t, "add", "api", "--dir", dir); err != nil {
		t.Fatalf("add api: %v", err)
	}

	out, err := run(t, "skills", "--dir", dir)
	if err != nil {
		t.Fatalf("skills: %v", err)
	}
	if !strings.Contains(out, "Applied 1 change(s)") {
		t.Fatalf("skills output = %q", out)
	}
	path := skillsPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	for _, want := range []string{"**http**", "**api**", "in-memory", "example.com/demo"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("generated skill is missing %q:\n%s", want, raw)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "skills", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat skills: %v", err)
	}
	if !strings.Contains(out, "up to date") {
		t.Fatalf("repeat skills output = %q", out)
	}
	repeat, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, repeat) {
		t.Fatal("repeat skills changed the file")
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(again.ModTime()) {
		t.Fatal("repeat skills rewrote an up-to-date file")
	}
}

func TestSkillsDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	out, err := run(t, "skills", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("skills --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, err := os.Stat(skillsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the skill file (stat err = %v)", err)
	}
}

func TestSkillsRefusesEditedFile(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "skills", "--dir", dir); err != nil {
		t.Fatalf("skills: %v", err)
	}
	path := skillsPath(dir)
	edited := []byte("# my own skill notes\n")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "skills", "--dir", dir)
	if err == nil {
		t.Fatal("expected skills to refuse an edited file")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("refusal error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, edited) {
		t.Fatal("edited skill file was overwritten")
	}
}

func TestSkillsTracksCapabilityChanges(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "skills", "--dir", dir); err != nil {
		t.Fatalf("skills: %v", err)
	}
	before, err := os.ReadFile(skillsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "**db**") {
		t.Fatalf("base project claims db is installed:\n%s", before)
	}

	if _, err := run(t, "add", "db", "--dir", dir); err != nil {
		t.Fatalf("add db: %v", err)
	}
	out, err := run(t, "skills", "--dir", dir)
	if err != nil {
		t.Fatalf("skills after add: %v", err)
	}
	if !strings.Contains(out, "update") {
		t.Fatalf("skills after add output = %q", out)
	}
	after, err := os.ReadFile(skillsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "**db**") {
		t.Fatalf("skill does not reflect the added capability:\n%s", after)
	}
}

func TestSkillsRefusesEditedFileAfterCapabilityAdd(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "skills", "--dir", dir); err != nil {
		t.Fatalf("skills: %v", err)
	}
	path := skillsPath(dir)
	if err := os.WriteFile(path, []byte("# user-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "add", "http", "--dir", dir); err != nil {
		t.Fatalf("add http: %v", err)
	}
	if _, err := run(t, "skills", "--dir", dir); err == nil {
		t.Fatal("expected skills to refuse an edited file after a capability add")
	}
}

func TestSkillsDoesNotLeakConfigSecrets(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	if _, err := run(t, "add", "http", "--dir", dir); err != nil {
		t.Fatalf("add http: %v", err)
	}
	configPath := filepath.Join(dir, "config.yml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.yml: %v", err)
	}
	secret := append(append([]byte(nil), raw...), []byte("  password: SUPERSECRET-SENTINEL\n")...)
	if err := os.WriteFile(configPath, secret, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "skills", "--dir", dir); err != nil {
		t.Fatalf("skills: %v", err)
	}
	body, err := os.ReadFile(skillsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "SUPERSECRET-SENTINEL") {
		t.Fatal("the generated skill file leaked a configuration secret")
	}
}

func TestSkillsRequiresProject(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "skills", "--dir", root); err == nil {
		t.Fatal("expected skills to fail outside a weld project")
	}
}
