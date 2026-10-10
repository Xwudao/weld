package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

// commandGraph is the stable command seam a Loom project gains.
func commandGraph(name string) string { return filepath.Join("internal", "di", name+"_graph.go") }

// assertLoomCommandSeam checks the generated command group is wired through its
// command-specific Loom graph: the graph exists, it includes the shared
// commonModule (and never snapshots the individual shared providers, so a
// capability installed later still reaches the command), the registration
// delegates to the generated initializer, the command package never imports the
// Loom runtime, and the graph never pulls in the HTTP server or the scheduler.
func assertLoomCommandSeam(t *testing.T, dir, name, initializer string) {
	t.Helper()
	graphPath := commandGraph(name)
	graph := readFile(t, filepath.Join(dir, graphPath))
	for _, want := range []string{"loom.WithContext()", "loom.Name(\"" + initializer + "\")", "commonModule"} {
		if !strings.Contains(graph, want) {
			t.Errorf("%s is missing %q:\n%s", graphPath, want, graph)
		}
	}
	for _, forbidden := range []string{
		"NewServer", "NewScheduler",
		"NewConfigLoader", "NewPool", "NewRedisClient", "NewMailSender", "NewObjectStore",
	} {
		if strings.Contains(graph, forbidden) {
			t.Errorf("%s snapshots %s; the shared providers belong to commonModule in di.go, and a CLI command must not start HTTP or cron:\n%s", graphPath, forbidden, graph)
		}
	}

	registration := readFile(t, filepath.Join(dir, "internal/app", name+"_command.go"))
	for _, want := range []string{"di." + initializer + "(ctx)"} {
		if !strings.Contains(registration, want) {
			t.Errorf("registration is missing %q:\n%s", want, registration)
		}
	}

	command := readFile(t, filepath.Join(dir, "internal/commands", name, "command.go"))
	if strings.Contains(command, "github.com/Xwudao/loom") {
		t.Errorf("the command package imports the Loom runtime:\n%s", command)
	}
	if !strings.Contains(command, "internal/commandkit") || !strings.Contains(command, "commandkit.Run(") {
		t.Errorf("the command package does not resolve through internal/commandkit:\n%s", command)
	}
}

// TestAddCommandThenLoomMigratesSeam proves installing Loom after an independent
// command upgrades the group to the command-specific graph instead of leaving it
// without dependency injection.
func TestAddCommandThenLoomMigratesSeam(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	addCommand(t, dir, "orders")
	add(t, dir, "loom")

	assertLoomCommandSeam(t, dir, "orders", "InitOrders")
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after loom migration: %+v", drift)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddLoomThenCommandGeneratesSeam proves adding a command to a Loom project
// creates the command-specific graph in one step.
func TestAddLoomThenCommandGeneratesSeam(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addCommand(t, dir, "orders")

	assertLoomCommandSeam(t, dir, "orders", "InitOrders")
	if !mustLoad(t, dir).HasCommand("orders") {
		t.Fatal("command was not recorded")
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddModuleThenLoomMigratesSeam proves installing Loom after a module-backed
// command upgrades that group too, keeping the module Service as its root.
func TestAddModuleThenLoomMigratesSeam(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	addModuleWithCommand(t, dir, "widget")
	add(t, dir, "loom")

	assertLoomCommandSeam(t, dir, "widget", "InitWidget")
	graph := readFile(t, filepath.Join(dir, commandGraph("widget")))
	if !strings.Contains(graph, "loom.Provide(module.NewService)") {
		t.Errorf("the module graph does not bind the module's NewService:\n%s", graph)
	}
	command := readFile(t, filepath.Join(dir, "internal/commands/widget/command.go"))
	if !strings.Contains(command, "Deps struct {") || !strings.Contains(command, "Service module.Service") {
		t.Errorf("the module command does not expose the Service seam:\n%s", command)
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after loom migration: %+v", drift)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddLoomThenModuleCommandGeneratesSeam proves a module command added to a
// Loom project is wired through the graph from the start, without the non-Loom
// route seam.
func TestAddLoomThenModuleCommandGeneratesSeam(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addModule(t, dir, "widget")
	addModuleWithCommand(t, dir, "widget")

	assertLoomCommandSeam(t, dir, "widget", "InitWidget")
	if _, err := os.Stat(filepath.Join(dir, "internal", "httpserver", "widget_route.go")); !os.IsNotExist(err) {
		t.Error("a Loom project wrote the non-Loom module route")
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddCommandOnBaseIsAtomicOnConflict proves a command installed on the bare
// base CLI plans Loom (and the config it requires) together with the command,
// and that a conflict anywhere fails the plan before anything is written: no
// partial Loom install, no command files.
func TestAddCommandOnBaseIsAtomicOnConflict(t *testing.T) {
	root := t.TempDir()
	dir := create(t, root)
	// A user file at the command target makes the command facet conflict.
	if err := os.MkdirAll(filepath.Join(dir, "internal", "commands", "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "internal", "commands", "orders", "command.go")
	if err := os.WriteFile(userFile, []byte("package orders\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBefore := readFile(t, filepath.Join(dir, project.ManifestName))

	_, err := AddCommand(Request{Dir: dir, Catalog: newCatalog()}, "orders")
	if err == nil {
		t.Fatal("expected a conflict for the unmanaged command file")
	}
	if readFile(t, userFile) != "package orders\n" {
		t.Fatal("the conflict overwrote the user's file")
	}
	if readFile(t, filepath.Join(dir, project.ManifestName)) != manifestBefore {
		t.Fatal("the conflict changed the manifest")
	}
	// Loom and config were planned together with the command, so the conflict
	// must leave none of them on disk.
	for _, path := range []string{"internal/di", "internal/config", "config.yml", "tools/loom"} {
		if _, statErr := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(statErr) {
			t.Errorf("the conflict still wrote %s: %v", path, statErr)
		}
	}
}

// TestLoomCommandGraphSurvivesLaterCapabilityAdds proves the command graph and
// command files are a stable seam: installing a later capability regenerates
// di.go (and may add its binding to the HTTP graph) but never rewrites the
// command graph, even when the user has edited it.
func TestLoomCommandGraphSurvivesLaterCapabilityAdds(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addCommand(t, dir, "orders")

	graphBefore := readFile(t, filepath.Join(dir, commandGraph("orders")))
	commandBefore := readFile(t, filepath.Join(dir, "internal/commands/orders/command.go"))
	// A user edit to the stable graph must survive too.
	edited := graphBefore + "\n// user binding, must survive later adds\n"
	if err := os.WriteFile(filepath.Join(dir, commandGraph("orders")), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "db")

	if got := readFile(t, filepath.Join(dir, commandGraph("orders"))); got != edited {
		t.Fatalf("a later capability add rewrote the command graph:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "internal/commands/orders/command.go")); got != commandBefore {
		t.Fatal("a later capability add rewrote the command files")
	}
	// The HTTP graph does gain the db binding through commonModule; the command
	// graph is a stable seam weld does not regenerate, but because it includes
	// commonModule the new binding still reaches the command.
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	if !strings.Contains(di, "var commonModule = loom.Module(") || !strings.Contains(di, "loom.Provide(NewPool)") {
		t.Errorf("the shared module did not gain the db binding:\n%s", di)
	}
	if got := readFile(t, filepath.Join(dir, commandGraph("orders"))); !strings.Contains(got, "commonModule") {
		t.Errorf("the command graph does not consume the shared module:\n%s", got)
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestLoomCommandGraphDeclaresOptionalBindingsButPrunes proves the shared
// commonModule declares the db binding when db is installed, but the generated
// initializer for the command constructs neither the pool nor the repository: a
// short command never opens a database.
func TestLoomCommandGraphDeclaresOptionalBindingsButPrunes(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "db")
	add(t, dir, "loom")
	addCommand(t, dir, "orders")

	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	for _, want := range []string{"loom.Provide(NewPool)", "loom.As[data.Repository](NewRepository)"} {
		if !strings.Contains(di, want) {
			t.Errorf("commonModule does not declare %q:\n%s", want, di)
		}
	}
	graph := readFile(t, filepath.Join(dir, commandGraph("orders")))
	if !strings.Contains(graph, "commonModule") {
		t.Errorf("the command graph does not include the shared module:\n%s", graph)
	}
	generated := readFile(t, filepath.Join(dir, "internal", "di", "loom_gen.go"))
	body := generated[strings.Index(generated, "func InitOrders("):]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	for _, forbidden := range []string{"NewPool", "NewRepository"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("InitOrders constructs the unused %s; a command must not open a database:\n%s", forbidden, body)
		}
	}
	gofmtCheck(t, dir)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestLoomCommandBareInvocationDoesNotStartServer proves a bare Loom command
// group (and help) never starts the HTTP server: with http installed the bare
// root serves, but `orders` shows its own help and exits without listening.
func TestLoomCommandBareInvocationDoesNotStartServer(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addCommand(t, dir, "orders")
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	env := serveEnv()
	binary := buildAppBinary(t, dir, env)

	out, err := exec.Command(binary, "orders").CombinedOutput()
	if err != nil {
		t.Fatalf("orders failed: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "listening") {
		t.Errorf("a bare command started the server:\n%s", text)
	}
	if !strings.Contains(text, "orders") {
		t.Errorf("a bare command did not show its own help:\n%s", text)
	}
}

// initializerBody returns the body of a generated initializer (the text between
// `func <name>(` and its closing brace), so a test can assert exactly what it
// constructs.
func initializerBody(t *testing.T, dir, name string) string {
	t.Helper()
	generated := readFile(t, filepath.Join(dir, "internal", "di", "loom_gen.go"))
	start := strings.Index(generated, "func "+name+"(")
	if start < 0 {
		t.Fatalf("loom_gen.go has no %s:\n%s", name, generated)
	}
	body := generated[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	return body
}

// editInPlace rewrites needle with replacement in path, failing when the needle
// is absent so a template change cannot make the test pass by not exercising
// anything.
func editInPlace(t *testing.T, path, needle, replacement string) {
	t.Helper()
	before := readFile(t, path)
	after := strings.Replace(before, needle, replacement, 1)
	if after == before {
		t.Fatalf("%s does not contain the expected text:\n%s", path, needle)
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoomCommandConsumesLateAddedProvider proves the shared-module fix: a
// capability installed after a command graph was written still reaches that
// command. It adds db and redis after the command graph exists, then makes the
// command root consume the late-added repository, regenerates the graph and
// builds — without weld rewriting the stable command graph. It also proves a
// bare command and help never resolve the graph and so never open the database
// or a Redis client.
func TestLoomCommandConsumesLateAddedProvider(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "loom")
	addCommand(t, dir, "orders")

	graphBefore := readFile(t, filepath.Join(dir, commandGraph("orders")))
	add(t, dir, "db")
	add(t, dir, "redis")
	if got := readFile(t, filepath.Join(dir, commandGraph("orders"))); got != graphBefore {
		t.Fatalf("the late capability adds rewrote the stable command graph:\n%s", got)
	}
	if !strings.Contains(graphBefore, "commonModule") {
		t.Fatalf("the command graph does not include the shared module:\n%s", graphBefore)
	}
	di := readFile(t, filepath.Join(dir, "internal/di/di.go"))
	for _, want := range []string{"loom.Provide(NewPool)", "loom.Provide(NewRedisClient)"} {
		if !strings.Contains(di, want) {
			t.Fatalf("commonModule did not gain %q:\n%s", want, di)
		}
	}
	pruned := initializerBody(t, dir, "InitOrders")
	for _, forbidden := range []string{"NewPool", "NewRepository", "NewRedisClient"} {
		if strings.Contains(pruned, forbidden) {
			t.Fatalf("InitOrders constructs the unused %s before anything consumes it:\n%s", forbidden, pruned)
		}
	}

	// Consume the late-added repository from the command root: only the
	// user-owned root changes, never the stable command graph.
	commandPath := filepath.Join(dir, "internal", "commands", "orders", "command.go")
	editInPlace(t, commandPath, "\tLogger *slog.Logger\n", "\tLogger *slog.Logger\n\tRepo data.Repository\n")
	editInPlace(t, commandPath, "func NewDeps(logger *slog.Logger) *Deps { return &Deps{Logger: logger} }", "func NewDeps(logger *slog.Logger, repo data.Repository) *Deps { return &Deps{Logger: logger, Repo: repo} }")
	editInPlace(t, commandPath, "\t\"example.com/demo/internal/commandkit\"\n", "\t\"example.com/demo/internal/commandkit\"\n\t\"example.com/demo/internal/data\"\n")

	if _, ok := runLoomTool(t, dir, "generate", "./internal/di"); !ok {
		t.Skip("cannot rebuild the pinned Loom generator (network/module cache unavailable)")
	}
	if body := initializerBody(t, dir, "InitOrders"); !strings.Contains(body, "NewRepository") {
		t.Fatalf("InitOrders did not pick up the late-added repository through commonModule:\n%s", body)
	}
	if !goModTidy(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
	goBuild(t, dir)
	runGo(t, dir, "test", "./internal/di/...")

	// A bare command and help only show the command's help; they never resolve
	// the graph, so the absent database credential cannot break them.
	binary := buildAppBinary(t, dir, serveEnv())
	for _, args := range [][]string{{"orders"}, {"orders", "--help"}} {
		out, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
		text := string(out)
		if strings.Contains(text, "listening") {
			t.Errorf("%v started the server:\n%s", args, text)
		}
		if strings.Contains(strings.ToLower(text), "database") || strings.Contains(strings.ToLower(text), "redis") {
			t.Errorf("%v reached the database or Redis:\n%s", args, text)
		}
	}
}

// TestAddAPIRefreshesGeneratedDITest proves the DI test is a generated file with
// no user-owned state: after a module's constructor changes and the user appends
// an assertion to di_test.go, adding api regenerates the test instead of
// preserving the edit. The regenerated test never names a module, so the changed
// NewService signature cannot break it, and the production graph still gains its
// API composition entry point.
func TestAddAPIRefreshesGeneratedDITest(t *testing.T) {
	goValidate := goValidateDir(t)
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	useLocalGoValidate(t, dir, goValidate)
	addModule(t, dir, "widget")
	add(t, dir, "db")

	servicePath := filepath.Join(dir, "internal", "modules", "widget", "service.go")
	editInPlace(t, servicePath,
		"func NewService() Service {\n\treturn NewDemoService()\n}",
		"func NewService(repo data.Repository) Service {\n\t_ = repo\n\treturn NewDemoService()\n}")
	editInPlace(t, servicePath,
		"\t\"strings\"\n",
		"\t\"strings\"\n\n\t\"example.com/demo/internal/data\"\n")

	diTestPath := filepath.Join(dir, "internal", "di", "di_test.go")
	const userTestMarker = "// user-owned module route assertion"
	if err := os.WriteFile(diTestPath, []byte(readFile(t, diTestPath)+"\n"+userTestMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	add(t, dir, "api")
	generated := readFile(t, diTestPath)
	if strings.Contains(generated, userTestMarker) {
		t.Fatal("weld add api preserved an edit to the generated DI test")
	}
	for _, forbidden := range []string{"internal/modules/widget", "FakeService"} {
		if strings.Contains(generated, forbidden) {
			t.Errorf("the regenerated di_test.go still names %q:\n%s", forbidden, generated)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "internal", "di", "di.go")), "NewServerWithAPI") {
		t.Fatal("the API graph did not gain its composition entry point")
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddModuleKeepsDITestModuleIndependent proves installing a module leaves the
// generated DI test byte-for-byte unchanged: the test is regenerated with the
// capability set, never with the module list, so adding a module cannot churn or
// break it. The production graph still lists the module.
func TestAddModuleKeepsDITestModuleIndependent(t *testing.T) {
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "api")

	diTestPath := filepath.Join(dir, "internal", "di", "di_test.go")
	before := readFile(t, diTestPath)

	addModule(t, dir, "widget")
	if after := readFile(t, diTestPath); after != before {
		t.Fatalf("adding a module changed the generated DI test:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "internal", "di", "di.go")), "widget") {
		t.Fatal("the production graph did not gain the module")
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add: %+v", drift)
	}
	gofmtCheck(t, dir)
	// This executes the generated DI test as well as the module and API tests, so
	// the composed graph is exercised, not merely compiled.
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project's dependencies (network/module cache unavailable)")
	}
}

// TestTwoModulesKeepGeneratedDITestValid catches template argument delimiters
// that a single-module project cannot expose. The composed graph routes both
// modules while the generated DI test stays module-independent.
func TestTwoModulesKeepGeneratedDITestValid(t *testing.T) {
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	addModule(t, dir, "first")
	addModule(t, dir, "second")
	add(t, dir, "api")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddOverwritesEditedDITest pins the ownership boundary: di_test.go is a
// generated file, so an edit to it does not survive a later add. Module-specific
// assertions belong in the module's own package test, which weld writes once and
// never regenerates. The regenerated test matches the manifest hash, so weld
// still owns it.
func TestAddOverwritesEditedDITest(t *testing.T) {
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	add(t, dir, "api")

	diTestPath := filepath.Join(dir, "internal", "di", "di_test.go")
	const userTestMarker = "// user-owned assertion"
	if err := os.WriteFile(diTestPath, []byte(readFile(t, diTestPath)+"\n"+userTestMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addModule(t, dir, "widget")
	if got := readFile(t, diTestPath); strings.Contains(got, userTestMarker) {
		t.Fatal("adding a module kept an edit to the generated DI test")
	}
	if drift := mustLoad(t, dir).Drift(dir); len(drift) != 0 {
		t.Fatalf("drift after add: %+v", drift)
	}
}

// TestLoomModuleServiceSignatureChangeCompiles proves the Loom seam is
// editable: after `weld add db`, changing the module's constructor to
// NewService(repo data.Repository) still compiles and tests, in both install
// orderings. The generated di_test.go never names a module, and the module's own
// tests build the demo through NewDemoService, so neither depends on NewService's
// signature.
func TestLoomModuleServiceSignatureChangeCompiles(t *testing.T) {
	loomToolAvailable(t)
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"module then loom", func(t *testing.T, dir string) {
			addModuleWithCommand(t, dir, "widget")
			add(t, dir, "loom")
		}},
		{"loom then module", func(t *testing.T, dir string) {
			add(t, dir, "loom")
			addModule(t, dir, "widget")
			addModuleWithCommand(t, dir, "widget")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			tc.setup(t, dir)
			add(t, dir, "db")

			servicePath := filepath.Join(dir, "internal", "modules", "widget", "service.go")
			editInPlace(t, servicePath,
				"func NewService() Service {\n\treturn NewDemoService()\n}",
				"func NewService(repo data.Repository) Service {\n\treturn NewDemoService()\n}")
			editInPlace(t, servicePath,
				"\t\"strings\"\n",
				"\t\"strings\"\n\n\t\"example.com/demo/internal/data\"\n")

			if _, ok := runLoomTool(t, dir, "generate", "./internal/di"); !ok {
				t.Skip("cannot rebuild the pinned Loom generator (network/module cache unavailable)")
			}
			if body := initializerBody(t, dir, "InitWidget"); !strings.Contains(body, "NewRepository") {
				t.Fatalf("InitWidget did not pick up the repository NewService now consumes:\n%s", body)
			}
			if !goModTidy(t, dir) {
				t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
			}
			goBuild(t, dir)
			runGo(t, dir, "test", "./internal/di/...")
			runGo(t, dir, "test", "./internal/modules/widget/...")
			runGo(t, dir, "test", "./internal/commands/widget/...")
		})
	}
}
