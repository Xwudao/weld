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
	if !strings.Contains(command, "type Lifecycle interface") || !strings.Contains(command, "func (g *group) resolve(") {
		t.Errorf("the command package has no lazy lifecycle seam:\n%s", command)
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
	editInPlace(t, commandPath, "type Deps struct{}", "type Deps struct{ Repo data.Repository }")
	editInPlace(t, commandPath, "func NewDeps() *Deps { return &Deps{} }", "func NewDeps(repo data.Repository) *Deps { return &Deps{Repo: repo} }")
	editInPlace(t, commandPath, "\t\"time\"\n", "\t\"time\"\n\n\t\"example.com/demo/internal/data\"\n")

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

// TestAddAPIPreservesCustomizedModuleDITest proves the cross-capability seam:
// after a module's constructor and DI test have been customized, adding api
// regenerates the production graph without replacing the project's test.
// The generated project still runs the API OpenAPI tests and the pre-existing
// module route tests together, so both route families remain buildable.
func TestAddAPIPreservesCustomizedModuleDITest(t *testing.T) {
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
	diTest := readFile(t, diTestPath) + "\n" + userTestMarker + "\n"
	if err := os.WriteFile(diTestPath, []byte(diTest), 0o644); err != nil {
		t.Fatal(err)
	}

	result := add(t, dir, "api")
	if got := readFile(t, diTestPath); !strings.Contains(got, userTestMarker) {
		t.Fatalf("weld add api replaced the customized DI test")
	}
	// Keeping a user-owned test is not silent: the add reports it, and with it the
	// boundary that weld cannot merge later changes into the test for the user.
	noted := false
	for _, note := range result.Notes {
		if strings.Contains(note, "internal/di/di_test.go") && strings.Contains(note, "edited locally") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("weld add api did not report the preserved DI test: %v", result.Notes)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "internal", "di", "di.go")), "NewServerWithAPI") {
		t.Fatal("the API graph did not gain its composition entry point")
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve the generated project's dependencies (network/module cache unavailable)")
	}
}

// TestAddRefreshesUneditedDITest proves an unmodified di_test.go is not frozen by
// the first add that changes it: whichever order api and a module are installed
// in, the generated test keeps following the installed capability set. The API
// composition entry point and the module fake must both be present, and the
// manifest must not have marked the unedited test preserve.
func TestAddRefreshesUneditedDITest(t *testing.T) {
	loomToolAvailable(t)
	cases := []struct {
		name string
		add  func(t *testing.T, dir string)
	}{
		{"api then module", func(t *testing.T, dir string) {
			add(t, dir, "loom")
			add(t, dir, "http")
			add(t, dir, "api")
			addModule(t, dir, "widget")
		}},
		{"module then api", func(t *testing.T, dir string) {
			add(t, dir, "loom")
			add(t, dir, "http")
			addModule(t, dir, "widget")
			add(t, dir, "api")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := create(t, root)
			tc.add(t, dir)

			manifest := mustLoad(t, dir)
			for _, file := range manifest.Files {
				if file.Path == "internal/di/di_test.go" && file.Preserve {
					t.Fatal("an unedited di_test.go was marked preserve: the first add froze it")
				}
			}
			diTest := readFile(t, filepath.Join(dir, "internal", "di", "di_test.go"))
			for _, want := range []string{
				"NewServerWithAPI",
				"widgetFakeService",
				"TestComposedMuxServesModulesWithoutSocket",
			} {
				if !strings.Contains(diTest, want) {
					t.Errorf("di_test.go was not refreshed with %q:\n%s", want, diTest)
				}
			}
			if drift := manifest.Drift(dir); len(drift) != 0 {
				t.Fatalf("drift after add: %+v", drift)
			}
			gofmtCheck(t, dir)
			// This executes the generated DI mux test as well as module/API tests:
			// /api/openapi.json must include the module's GET route and response
			// schema in both install orders, not merely compile the graph.
			if !buildAndTestGeneratedProject(t, dir) {
				t.Skip("cannot resolve generated project's dependencies (network/module cache unavailable)")
			}
			// A default build rejects the endpoint. The opt-in build must
			// actually serve and parse the composed module document.
			runGo(t, dir, "test", "-tags", "openapi", "./internal/api", "./internal/di")
		})
	}
}

// TestTwoModulesKeepGeneratedDITestValid catches template argument delimiters
// that a single-module project cannot expose. Both module routes must appear
// in the real composed document with an opt-in development build.
func TestTwoModulesKeepGeneratedDITestValid(t *testing.T) {
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	addModule(t, dir, "first")
	addModule(t, dir, "second")
	add(t, dir, "api")
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project's dependencies (network/module cache unavailable)")
	}
	runGo(t, dir, "test", "-tags", "openapi", "./internal/api", "./internal/di")
}

// TestAddKeepsEditedDITestAndDocumentsTheBoundary proves the other half of the
// seam: once the user has edited di_test.go it stays preserved across a later
// module add too, and the add reports it. weld deliberately does not merge or
// overwrite the user's assertions, so a module whose Service signature changes
// NewServer remains a manual update; this test pins that boundary instead of
// letting a later add silently rewrite the file.
func TestAddKeepsEditedDITestAndDocumentsTheBoundary(t *testing.T) {
	loomToolAvailable(t)
	root := t.TempDir()
	dir := create(t, root)
	add(t, dir, "http")
	add(t, dir, "api")

	diTestPath := filepath.Join(dir, "internal", "di", "di_test.go")
	const userTestMarker = "// user-owned assertion"
	if err := os.WriteFile(diTestPath, []byte(readFile(t, diTestPath)+"\n"+userTestMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := addModule(t, dir, "widget")
	if got := readFile(t, diTestPath); !strings.Contains(got, userTestMarker) {
		t.Fatal("adding a module replaced the edited DI test")
	}
	noted := false
	for _, note := range result.Notes {
		if strings.Contains(note, "internal/di/di_test.go") && strings.Contains(note, "edited locally") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("adding a module did not report the preserved DI test: %v", result.Notes)
	}
	preserved := false
	for _, file := range mustLoad(t, dir).Files {
		if file.Path == "internal/di/di_test.go" && file.Preserve {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("the edited DI test was not recorded as preserved")
	}
}

// TestLoomModuleServiceSignatureChangeCompiles proves the Loom seam is
// editable: after `weld add db`, changing the module's constructor to
// NewService(repo data.Repository) still compiles and tests, in both install
// orderings. The regenerated di_test.go serves the module through a fake, the
// non-Loom route written before Loom calls the stable NewDemoService, and the
// module's own tests use NewDemoService, so none of them depend on NewService's
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
