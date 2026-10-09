package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/template"
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

func TestAddHelpListsEmbeddedCapabilities(t *testing.T) {
	out, err := run(t, "add", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Available capabilities:") || !strings.Contains(out, "weld list") {
		t.Fatalf("add help lacks capability guidance: %s", out)
	}
	catalog := template.Load()
	names, err := catalog.Names()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		capability, err := catalog.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if capability.Kind == template.KindAdd && !strings.Contains(out, "  "+name+" ") {
			t.Errorf("add help omitted %s", name)
		}
	}
	if strings.Contains(out, "  base ") {
		t.Fatal("base is not an additive capability")
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
	// redis is independent of the HTTP and database capabilities, but installing
	// it also installs loom (and config), just without the HTTP surface: the
	// dependency graph is CLI-only.
	for _, path := range []string{"internal/httpserver", "internal/api", "internal/data", "internal/web"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add redis created %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "di", "di.go")); err != nil {
		t.Fatalf("add redis did not install the CLI-only Loom graph: %v", err)
	}
	diRaw, err := os.ReadFile(filepath.Join(dir, "internal", "di", "di.go"))
	if err != nil {
		t.Fatalf("read di.go: %v", err)
	}
	di := string(diRaw)
	if !strings.Contains(di, "var commonModule = loom.Module(") {
		t.Errorf("the CLI-only graph has no commonModule:\n%s", di)
	}
	if strings.Contains(di, "internal/httpserver") || strings.Contains(di, "NewServer") {
		t.Errorf("the CLI-only graph pulls in the HTTP server:\n%s", di)
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

func TestAddModuleCommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "module", "widget", "--dir", dir)
	if err != nil {
		t.Fatalf("add module: %v", err)
	}
	if !strings.Contains(out, "internal/modules/widget/module.go") {
		t.Fatalf("add module output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "modules", "widget", "module.go")); err != nil {
		t.Fatalf("add module did not write the module: %v", err)
	}
	// The module needs http, which the same command installs without being asked.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "http.go")); err != nil {
		t.Fatalf("add module did not resolve the http dependency: %v", err)
	}

	// Repeating add is a no-op that still succeeds.
	out, err = run(t, "add", "module", "widget", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add module: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add module output = %q", out)
	}

	// list reports the installed module.
	listOut, err := run(t, "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "widget") {
		t.Fatalf("list does not report the module: %q", listOut)
	}

	// A bad name and a missing name are rejected.
	if _, err := run(t, "add", "module", "items", "--dir", dir); err == nil {
		t.Fatal("expected add module items to fail")
	}
	if _, err := run(t, "add", "module", "--dir", dir); err == nil {
		t.Fatal("expected add module without a name to fail")
	}
}

func TestAddModuleDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "module", "widget", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add module --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "modules")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the module")
	}
}

// TestBareInvocationShowsHelpToStdout locks the Cobra root behavior: a bare
// invocation shows help on stdout and succeeds rather than failing on a
// missing subcommand.
func TestBareInvocationShowsHelpToStdout(t *testing.T) {
	out, err := run(t)
	if err != nil {
		t.Fatalf("bare invocation: %v", err)
	}
	for _, want := range []string{"Usage:", progName, "Capabilities:"} {
		if !strings.Contains(out, want) {
			t.Errorf("bare invocation help is missing %q:\n%s", want, out)
		}
	}
}

// TestHelpFlagsReturnNilAndPrintHelp covers `help`, `--help` and `-h`: each
// must print help and report success, so the migration keeps the earlier help
// semantics alongside the new Cobra tree.
func TestHelpFlagsReturnNilAndPrintHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "Capabilities:") {
			t.Errorf("%v help output = %q", args, out)
		}
	}
}

// TestVersionFlagAgreesWithVersionCommand proves `weld version`, `weld
// --version` and `weld -v` print one line, so the Cobra version flag matches
// the explicit subcommand.
func TestVersionFlagAgreesWithVersionCommand(t *testing.T) {
	command, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != command {
			t.Errorf("%v output = %q, want %q", args, out, command)
		}
	}
}

// TestListAlias covers the `ls` alias the earlier dispatcher accepted.
func TestListAlias(t *testing.T) {
	long, err := run(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	short, err := run(t, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if short != long {
		t.Errorf("ls output = %q, want the list output %q", short, long)
	}
}

// TestInterspersedFlags exercises the flag handling the migration must keep:
// flags may follow or precede the positional argument, and a dry run reaches
// the same plan either way.
func TestInterspersedFlags(t *testing.T) {
	root := t.TempDir()
	before, err := run(t, "new", "--dir", root, "--module", "example.com/demo", "demo", "--dry-run")
	if err != nil {
		t.Fatalf("flags before positional: %v", err)
	}
	after, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root, "--dry-run")
	if err != nil {
		t.Fatalf("flags after positional: %v", err)
	}
	if before != after {
		t.Errorf("plan depends on flag position:\nbefore=%q\nafter=%q", before, after)
	}
	if !strings.Contains(after, "dry run") {
		t.Errorf("dry-run output = %q", after)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatal("a dry run created the project")
	}
}

// TestUnknownCommandError locks the one-error message shape: Run returns the
// error rather than printing it, and it names the offending command and the
// help suggestion.
func TestUnknownCommandError(t *testing.T) {
	_, err := run(t, "bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) || !strings.Contains(err.Error(), "weld help") {
		t.Errorf("unknown command error = %v", err)
	}
}

// TestUsageErrorsNameTheCommand covers the argument validation the earlier
// dispatcher reported: a missing name, capability or module name is a usage
// error, not a panic or a silent no-op.
func TestUsageErrorsNameTheCommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"new"}, "weld new <name>"},
		{[]string{"new", "a", "b", "--dir", root}, "weld new <name>"},
		{[]string{"add", "--dir", dir}, "weld add <capability>"},
		{[]string{"add", "module", "--dir", dir}, "weld add module <name>"},
	}
	for _, tc := range cases {
		_, err := run(t, tc.args...)
		if err == nil {
			t.Errorf("%v: expected a usage error", tc.args)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v error = %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
}

func TestAddCommandSubcommand(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "command", "orders", "--dir", dir)
	if err != nil {
		t.Fatalf("add command: %v", err)
	}
	if !strings.Contains(out, "internal/commands/orders/command.go") {
		t.Fatalf("add command output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands", "orders", "command.go")); err != nil {
		t.Fatalf("add command did not write the command group: %v", err)
	}
	// A command group is independent: it installs no HTTP server.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver")); !os.IsNotExist(err) {
		t.Fatal("add command created the HTTP server")
	}

	// Repeating add is a no-op that still succeeds.
	out, err = run(t, "add", "command", "orders", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add command: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add command output = %q", out)
	}

	// list reports the installed command.
	listOut, err := run(t, "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "Commands in demo") || !strings.Contains(listOut, "orders") {
		t.Fatalf("list does not report the command: %q", listOut)
	}

	// A reserved name and a missing name are rejected.
	if _, err := run(t, "add", "command", "serve", "--dir", dir); err == nil {
		t.Fatal("expected add command serve to fail")
	}
	if _, err := run(t, "add", "command", "--dir", dir); err == nil {
		t.Fatal("expected add command without a name to fail")
	}
}

func TestAddModuleWithCommandFlag(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "module", "widget", "--command", "--dir", dir)
	if err != nil {
		t.Fatalf("add module --command: %v", err)
	}
	for _, want := range []string{"internal/modules/widget/module.go", "internal/commands/widget/command.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("add module --command output is missing %q: %q", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands", "widget", "command.go")); err != nil {
		t.Fatalf("add module --command did not write the command group: %v", err)
	}

	// Repeating is a no-op.
	out, err = run(t, "add", "module", "widget", "--command", "--dir", dir)
	if err != nil {
		t.Fatalf("repeat add module --command: %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Fatalf("repeat add module --command output = %q", out)
	}
}

func TestAddModuleCommandDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, "new", "demo", "--module", "example.com/demo", "--dir", root); err != nil {
		t.Fatalf("new: %v", err)
	}
	dir := filepath.Join(root, "demo")

	out, err := run(t, "add", "module", "widget", "--command", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("add module --command --dry-run: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the command group")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "modules")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the module")
	}
}
