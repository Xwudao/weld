package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// addCommand plans and applies `weld add command <name>`.
func addCommand(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, name)
	if err != nil {
		t.Fatalf("AddCommand %s: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add command %s: %v", name, err)
	}
	return result
}

// addModuleWithCommand plans and applies `weld add module <name> --command`.
func addModuleWithCommand(t *testing.T, dir, name string) *Result {
	t.Helper()
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog(), Command: true}, name)
	if err != nil {
		t.Fatalf("AddModule %s --command: %v", name, err)
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("Apply add module %s --command: %v", name, err)
	}
	return result
}

func TestValidateCommandName(t *testing.T) {
	for _, name := range []string{"orders", "cleanup", "a1", "catalog2"} {
		if err := ValidateCommandName(name); err != nil {
			t.Errorf("ValidateCommandName(%q) = %v, want nil", name, err)
		}
	}
	// Not a canonical Go package, a Go keyword, or a reserved root command.
	for _, name := range []string{"", "Orders", "order-1", "1orders", "_orders", "orders_1", "range", "func", "serve", "help", "version"} {
		if err := ValidateCommandName(name); err == nil {
			t.Errorf("ValidateCommandName(%q) = nil, want an error", name)
		}
	}
}

// TestAddCommandGeneratesIndependentGroup proves `weld add command` writes an
// independent root command group: no HTTP and no database, registered through
// the base app's RegisterCommand seam. Every non-base capability is a Loom
// project, so the command also installs loom (and the config it requires).
func TestAddCommandGeneratesIndependentGroup(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	baseApp := readFile(t, filepath.Join(dir, "internal/app/app.go"))

	result := addCommand(t, dir, "orders")
	if got, want := strings.Join(result.Installed, ","), "config,loom"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/commands/orders/command.go",
		"internal/commands/orders/command_test.go",
		"internal/commands/orders/README.md",
		"internal/app/orders_command.go",
		"internal/config/config.go",
		"internal/di/di.go",
		"internal/di/orders_graph.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// A command group is independent of the HTTP server and the business modules.
	for _, path := range []string{"internal/httpserver", "internal/modules", "internal/app/serve.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("add command created %s", path)
		}
	}

	// The registration uses the one shared root-command seam and delegates to the
	// command-specific graph, not a new dispatcher.
	registration := readFile(t, filepath.Join(dir, "internal/app/orders_command.go"))
	for _, want := range []string{"RegisterCommand(newOrdersCommand)", "di.InitOrders(ctx)"} {
		if !strings.Contains(registration, want) {
			t.Errorf("registration is missing %q:\n%s", want, registration)
		}
	}
	if strings.Contains(registration, "func main") {
		t.Errorf("registration file defines its own dispatcher:\n%s", registration)
	}
	// The base app remains the one command tree: the capability adds files, it
	// does not edit the dispatcher.
	if readFile(t, filepath.Join(dir, "internal/app/app.go")) != baseApp {
		t.Error("add command modified the base app dispatcher")
	}

	manifest := mustLoad(t, dir)
	if !manifest.HasCommand("orders") || manifest.CommandVersion("orders") == "" {
		t.Fatalf("command not recorded: %+v", manifest.Commands)
	}
	if manifest.HasModule("orders") {
		t.Fatalf("command recorded as a module: %+v", manifest.Modules)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add command: %+v", manifest.Drift(dir))
	}
	gofmtCheck(t, dir)
}

func TestAddCommandIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addCommand(t, dir, "orders")

	before := readFile(t, filepath.Join(dir, "internal/commands/orders/command.go"))
	result, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, "orders")
	if err != nil {
		t.Fatalf("second AddCommand: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second AddCommand planned %d operations, want 0", len(result.Operations))
	}
	if len(result.Notes) == 0 {
		t.Fatal("second AddCommand produced no explanation")
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if readFile(t, filepath.Join(dir, "internal/commands/orders/command.go")) != before {
		t.Fatal("a repeat add rewrote the command file")
	}
}

func TestAddCommandIsDryRunnable(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))
	result, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, "orders")
	if err != nil {
		t.Fatalf("AddCommand: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected a non-empty plan")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("planning wrote the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands")); !os.IsNotExist(err) {
		t.Fatal("planning wrote command files")
	}
}

func TestAddCommandRejectsReservedNameBeforeWriting(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))
	for _, name := range []string{"serve", "help", "version", "Orders"} {
		if _, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, name); err == nil {
			t.Errorf("AddCommand(%q) = nil, want an error", name)
		}
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("a rejected command wrote the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands")); !os.IsNotExist(err) {
		t.Fatal("a rejected command wrote files")
	}
}

// TestAddCommandRejectsModuleCollision proves a name is either a module or a
// command, never both, in both creation orders.
func TestAddCommandRejectsModuleCollision(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")

	if _, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, "widget"); err == nil {
		t.Fatal("expected a conflict adding a command for an existing module name")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands", "widget")); !os.IsNotExist(err) {
		t.Fatal("a rejected command wrote files")
	}

	other := create(t, t.TempDir())
	addCommand(t, other, "reports")
	if _, err := AddModule(Request{Dir: other, Catalog: newCatalog()}, "reports"); err == nil {
		t.Fatal("expected a conflict adding a module for an existing command name")
	}
}

// TestAddModuleWithCommandGeneratesBoth proves `weld add module --command`
// writes the HTTP module (installing http and loom) and a command group backed
// by the same Service.
func TestAddModuleWithCommandGeneratesBoth(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)

	result := addModuleWithCommand(t, dir, "widget")
	if got, want := strings.Join(result.Installed, ","), "config,loom,http"; got != want {
		t.Fatalf("installed = %q, want %q", got, want)
	}
	for _, path := range []string{
		"internal/modules/widget/service.go",
		"internal/modules/widget/module.go",
		"internal/commands/widget/command.go",
		"internal/commands/widget/command_test.go",
		"internal/app/widget_command.go",
		"internal/di/di.go",
		"internal/di/widget_graph.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	// A module is registered through the graph, never a separate route seam.
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "widget_route.go")); !os.IsNotExist(err) {
		t.Error("the module wrote the retired non-Loom route seam")
	}

	// The command group is backed by the module's own Service and NewService.
	command := readFile(t, filepath.Join(dir, "internal/commands/widget/command.go"))
	for _, want := range []string{"module.Service", "func NewDeps(service module.Service, logger *slog.Logger)"} {
		if !strings.Contains(command, want) {
			t.Errorf("module command is missing %q:\n%s", want, command)
		}
	}
	registration := readFile(t, filepath.Join(dir, "internal/app/widget_command.go"))
	if !strings.Contains(registration, "di.InitWidget(ctx)") {
		t.Errorf("registration does not delegate to the module's command graph:\n%s", registration)
	}

	manifest := mustLoad(t, dir)
	if !manifest.HasModule("widget") || !manifest.HasCommand("widget") {
		t.Fatalf("module/command not both recorded: modules=%v commands=%v", manifest.Modules, manifest.Commands)
	}
	if len(manifest.Drift(dir)) != 0 {
		t.Fatalf("drift after add module --command: %+v", manifest.Drift(dir))
	}
	gofmtCheck(t, dir)
}

// TestAddModuleCommandUpgradeAddsOnlyCommandFiles proves that adding the command
// facet to a module that already exists writes only the command files and never
// rewrites the user's module service or handler.
func TestAddModuleCommandUpgradeAddsOnlyCommandFiles(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModule(t, dir, "widget")

	servicePath := filepath.Join(dir, "internal", "modules", "widget", "service.go")
	edited := readFile(t, servicePath) + "\n// user wiring, must survive the upgrade\n"
	if err := os.WriteFile(servicePath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	result := addModuleWithCommand(t, dir, "widget")
	if got := strings.Join(result.Installed, ","); got != "" {
		t.Fatalf("upgrade installed capabilities %q, want none", got)
	}
	for _, operation := range result.Operations {
		if operation.Path == "internal/modules/widget/service.go" || operation.Path == "internal/modules/widget/module.go" {
			t.Fatalf("upgrade rewrote a module file: %s", operation.Path)
		}
	}
	if got := readFile(t, servicePath); got != edited {
		t.Fatalf("upgrade rewrote the user's module service:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands", "widget", "command.go")); err != nil {
		t.Fatalf("upgrade did not write the command group: %v", err)
	}
	if !mustLoad(t, dir).HasCommand("widget") {
		t.Fatal("upgrade did not record the command")
	}
	gofmtCheck(t, dir)
}

func TestAddModuleCommandIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModuleWithCommand(t, dir, "widget")

	before := readFile(t, filepath.Join(dir, "internal/commands/widget/command.go"))
	result, err := AddModule(Request{Dir: dir, Catalog: newCatalog(), Command: true}, "widget")
	if err != nil {
		t.Fatalf("second AddModule --command: %v", err)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("second AddModule --command planned %d operations, want 0", len(result.Operations))
	}
	if err := result.Apply(); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if readFile(t, filepath.Join(dir, "internal/commands/widget/command.go")) != before {
		t.Fatal("a repeat add rewrote the command file")
	}
}

// TestAddCommandGeneratedProjectBuildsAndTests compiles and runs a real
// generated base + command project. Its generated command factory test runs the
// unknown-subcommand path in process.
func TestAddCommandGeneratedProjectBuildsAndTests(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addCommand(t, dir, "orders")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddModuleCommandGeneratedProjectBuildsAndTests compiles and runs a real
// generated module + command project, exercising the module-backed command
// factory against the module's own Service.
func TestAddModuleCommandGeneratedProjectBuildsAndTests(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	addModuleWithCommand(t, dir, "widget")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddModuleCommandWithLoom proves a Loom project registers the module
// through the graph and still gets the module-backed command group, which shares
// the module's NewService.
func TestAddModuleCommandWithLoom(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addModuleWithCommand(t, dir, "widget")

	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "widget_route.go")); !os.IsNotExist(err) {
		t.Error("a Loom project also wrote the non-Loom route seam")
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, `widget.Register(base.Group(httpserver.APIPrefix+"/widget"), widgetService)`) {
		t.Errorf("the Loom graph does not register the module:\n%s", di)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "commands", "widget", "command.go")); err != nil {
		t.Fatalf("the command group is missing: %v", err)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift: %+v", drift)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestCustomCommandDoesNotStartServer proves a generated command group never
// starts the HTTP server: with http installed a bare invocation serves, but a
// custom command shows its own help and exits without binding a socket.
func TestCustomCommandDoesNotStartServer(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	addCommand(t, dir, "orders")
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)

	out, err := exec.Command(binary, "orders").CombinedOutput()
	if err != nil {
		t.Fatalf("custom command failed: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "listening") {
		t.Errorf("a custom command started the server:\n%s", text)
	}
	if !strings.Contains(text, "orders") {
		t.Errorf("a custom command did not show its own help:\n%s", text)
	}
}
