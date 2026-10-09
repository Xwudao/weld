package template

import (
	"strings"
	"testing"
)

func TestCatalogListsBaseAndWeb(t *testing.T) {
	catalog := Load()
	names, err := catalog.Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	want := map[string]bool{"base": false, "config": false, "http": false, "web": false, "api": false, "db": false, "loom": false, "redis": false, "mail": false, "storage": false, "cron": false}
	for _, name := range names {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("capability %q missing from %v", name, names)
		}
	}
}

func TestBaseCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("base")
	if err != nil {
		t.Fatalf("Get base: %v", err)
	}
	if capability.Kind != KindBase {
		t.Errorf("kind = %q, want %q", capability.Kind, KindBase)
	}
	if len(capability.Files) == 0 {
		t.Fatal("base has no files")
	}
	for _, file := range capability.Files {
		content, err := capability.ReadFile(file)
		if err != nil {
			t.Errorf("read %s: %v", file.Path, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("payload for %s is empty", file.Path)
		}
	}
}

func TestWebCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("web")
	if err != nil {
		t.Fatalf("Get web: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "http" {
		t.Errorf("requires = %v, want [http]", capability.Requires)
	}
	if len(capability.Patches) != 2 {
		t.Errorf("patches = %d, want 2 (Makefile web and the git-ignore web region)", len(capability.Patches))
	}
}

func TestHTTPCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("http")
	if err != nil {
		t.Fatalf("Get http: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "loom"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	var configLocal, configExample bool
	for _, patch := range capability.Patches {
		switch {
		case patch.Path == "config.yml" && patch.Marker == "config":
			configLocal = true
			if patch.Bootstrap != "config.example.yml" {
				t.Errorf("http config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
			}
		case patch.Path == "config.example.yml" && patch.Marker == "config":
			configExample = true
		}
	}
	if !configLocal || !configExample {
		t.Errorf("http patches = %+v, want config.yml and config.example.yml", capability.Patches)
	}
}

// TestConfigCapabilityLoads pins the config capability's shape: it is additive,
// needs only base, ships the shared loader and both the local and example YAML
// files, and patches the git-ignore and go.mod dependency regions.
func TestConfigCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("config")
	if err != nil {
		t.Fatalf("Get config: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "base" {
		t.Errorf("requires = %v, want [base]", capability.Requires)
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{"internal/config/config.go", "internal/config/config_test.go", "config.example.yml", "config.yml"} {
		if !paths[want] {
			t.Errorf("config capability does not ship %s", want)
		}
	}
	var gitignore, deps bool
	for _, patch := range capability.Patches {
		switch {
		case patch.Path == ".gitignore" && patch.Marker == "config":
			gitignore = true
		case patch.Path == "go.mod" && patch.Marker == "deps":
			deps = true
		}
	}
	if !gitignore {
		t.Error("config does not patch the git-ignore region")
	}
	if !deps {
		t.Error("config does not patch the go.mod dependency region")
	}
}

func TestAPICapabilityLoads(t *testing.T) {
	capability, err := Load().Get("api")
	if err != nil {
		t.Fatalf("Get api: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "http" {
		t.Errorf("requires = %v, want [http]", capability.Requires)
	}
	if len(capability.Patches) != 1 {
		t.Errorf("patches = %d, want 1 (go.mod deps)", len(capability.Patches))
	}
}

// TestDBCapabilityLoads pins the db capability's shape: it is additive, needs
// loom (which needs config), patches exactly the go.mod dependency region, the
// Makefile db region and the two config regions, and declares the tracked
// example to restore the git-ignored local file from.
func TestDBCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("db")
	if err != nil {
		t.Fatalf("Get db: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "loom"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	if len(capability.Patches) != 4 {
		t.Errorf("patches = %d, want 4 (go.mod deps, Makefile db and the two config regions)", len(capability.Patches))
	}
	for _, patch := range capability.Patches {
		if patch.Path != "config.yml" || patch.Marker != "config" {
			continue
		}
		if patch.Bootstrap != "config.example.yml" {
			t.Errorf("db config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
		}
	}
}

// TestRedisCapabilityLoads pins the redis capability's shape: it requires loom
// (which pulls config; never http, db or api), ships the client package and the
// config extension, and declares its Loom provider seam guarded on loom.
func TestRedisCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("redis")
	if err != nil {
		t.Fatalf("Get redis: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "loom"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	for _, forbidden := range []string{"http", "db", "api"} {
		for _, required := range capability.Requires {
			if required == forbidden {
				t.Errorf("redis requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/config/redis.go",
		"internal/redisclient/redisclient.go",
		"internal/redisclient/redisclient_test.go",
		"internal/di/redis_provider.go",
	} {
		if !paths[want] {
			t.Errorf("redis capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range capability.Files {
		if file.Path == "internal/di/redis_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("redis redis_provider.go is not guarded by when: [loom]: %+v", capability.Files)
	}
	markers := map[string]bool{}
	for _, patch := range capability.Patches {
		if patch.Path == "internal/config/config.go" {
			markers[patch.Marker] = true
		}
		if patch.Path == "config.yml" && patch.Marker == "config" && patch.Bootstrap != "config.example.yml" {
			t.Errorf("redis config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
		}
	}
	for _, want := range []string{"configfields", "configenv", "configdefaults"} {
		if !markers[want] {
			t.Errorf("redis does not patch the config.go %s extension point: %+v", want, capability.Patches)
		}
	}
}

// TestCronCapabilityLoads pins the cron capability's shape: it is additive,
// requires http (so the scheduler has a lifecycle through the Loom server
// graph), ships the scheduler plus a stable registration file, and declares its
// Loom provider seam guarded on loom.
func TestCronCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("cron")
	if err != nil {
		t.Fatalf("Get cron: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "http"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	for _, forbidden := range []string{"web", "api", "db", "loom"} {
		for _, required := range capability.Requires {
			if required == forbidden {
				t.Errorf("cron requires %q", forbidden)
			}
		}
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/cron/cron.go",
		"internal/cron/register.go",
		"internal/config/cron.go",
		"internal/di/cron_provider.go",
	} {
		if !paths[want] {
			t.Errorf("cron capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range capability.Files {
		if file.Path == "internal/di/cron_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("cron cron_provider.go is not guarded by when: [loom]: %+v", capability.Files)
	}
	markers := map[string]bool{}
	for _, patch := range capability.Patches {
		if patch.Path == "internal/config/config.go" {
			markers[patch.Marker] = true
		}
		if patch.Path == "config.yml" && patch.Marker == "config" && patch.Bootstrap != "config.example.yml" {
			t.Errorf("cron config.yml patch bootstrap = %q, want config.example.yml", patch.Bootstrap)
		}
	}
	for _, want := range []string{"configfields", "configenv", "configdefaults"} {
		if !markers[want] {
			t.Errorf("cron does not patch the config.go %s extension point: %+v", want, capability.Patches)
		}
	}
}

// TestMailCapabilityLoads pins the mail capability's shape: it is additive, needs
// only base and config, and declares its Loom provider seam guarded on loom.
func TestMailCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("mail")
	if err != nil {
		t.Fatalf("Get mail: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "loom"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/config/mail.go",
		"internal/mailsender/mailsender.go",
		"internal/mailsender/mailsender_test.go",
		"internal/di/mail_provider.go",
	} {
		if !paths[want] {
			t.Errorf("mail capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range capability.Files {
		if file.Path == "internal/di/mail_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("mail mail_provider.go is not guarded by when: [loom]: %+v", capability.Files)
	}
}

// TestStorageCapabilityLoads pins the storage capability's shape and its
// guarded Loom provider seam, mirroring the redis contract.
func TestStorageCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("storage")
	if err != nil {
		t.Fatalf("Get storage: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if got, want := strings.Join(capability.Requires, ","), "loom"; got != want {
		t.Errorf("requires = %v, want %q", capability.Requires, want)
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"internal/config/storage.go",
		"internal/objectstore/objectstore.go",
		"internal/objectstore/objectstore_test.go",
		"internal/di/storage_provider.go",
	} {
		if !paths[want] {
			t.Errorf("storage capability does not ship %s", want)
		}
	}
	var providerSeam bool
	for _, file := range capability.Files {
		if file.Path == "internal/di/storage_provider.go" {
			providerSeam = len(file.When) == 1 && file.When[0] == "loom"
		}
	}
	if !providerSeam {
		t.Errorf("storage storage_provider.go is not guarded by when: [loom]: %+v", capability.Files)
	}
}

func TestUnknownCapabilityErrors(t *testing.T) {
	if _, err := Load().Get("nope"); err == nil {
		t.Fatal("expected error for unknown capability")
	}
}

func TestRenderSubstitutesTokens(t *testing.T) {
	got := string(Render([]byte("module __module__ name __name__ v__version__ {{keep}}"), Vars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "9.9.9",
	}))
	want := "module example.com/demo name demo v9.9.9 {{keep}}"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderLeavesBracesUntouched(t *testing.T) {
	in := []byte(`const x = { a: { b: 1 } }`)
	out := string(Render(in, Vars{Name: "demo"}))
	if strings.Contains(out, "\x00") {
		t.Fatal("render corrupted braces")
	}
	if out != string(in) {
		t.Fatalf("Render changed brace-heavy content: %q", out)
	}
}

// TestLoomCapabilityLoads pins the loom capability's shape: it requires config,
// declares a capability-aware DI graph, and raises the go directive region. The
// server graph and the serve wiring belong to the http capability.
func TestLoomCapabilityLoads(t *testing.T) {
	capability, err := Load().Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	if capability.Kind != KindAdd {
		t.Errorf("kind = %q, want %q", capability.Kind, KindAdd)
	}
	if len(capability.Requires) != 1 || capability.Requires[0] != "config" {
		t.Errorf("requires = %v, want [config]", capability.Requires)
	}
	if capability.DI == nil || capability.DI.Dir != "internal/di" || capability.DI.Source == "" {
		t.Fatalf("loom DI spec = %+v", capability.DI)
	}
	paths := map[string]bool{}
	for _, file := range capability.Files {
		paths[file.Path] = true
	}
	for _, want := range []string{
		"tools/loom/go.mod",
		"tools/loom/go.sum",
	} {
		if !paths[want] {
			t.Errorf("loom capability does not ship %s", want)
		}
	}
	for _, forbidden := range []string{"internal/app/serve_loom.go", "internal/di/api_provider.go", "internal/di/redis_provider.go", "internal/di/mail_provider.go", "internal/di/storage_provider.go", "internal/di/cron_provider.go"} {
		if paths[forbidden] {
			t.Errorf("loom capability still ships %s; the provider seam belongs to the capability", forbidden)
		}
	}
	var goversion bool
	for _, patch := range capability.Patches {
		switch {
		case patch.Path == "go.mod" && patch.Marker == "goversion":
			goversion = patch.Mode == "replace"
		case patch.Path == "internal/app/serve.go":
			t.Errorf("loom still patches the plain serve registration: %+v", patch)
		}
	}
	if !goversion {
		t.Error("loom does not replace the go.mod goversion region")
	}
}

// TestRenderDIGraphIsCapabilityAware checks that the same template produces a
// different, gofmt-clean graph for different installed capability sets.
func TestRenderDIGraphIsCapabilityAware(t *testing.T) {
	capability, err := Load().Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	vars := DITemplateVars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "0.1.0",
		Caps:    CapabilitySet{"base": true, "http": true, "loom": true},
	}
	// A CLI-only project (no http) declares commonModule but no server graph, no
	// InitApp and no httpserver import, so a short command never compiles in the
	// HTTP surface.
	cliOnly, err := capability.RenderDIGraph(DITemplateVars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "0.1.0",
		Caps:    CapabilitySet{"base": true, "config": true, "loom": true},
	})
	if err != nil {
		t.Fatalf("RenderDIGraph(cli-only): %v", err)
	}
	for _, want := range []string{"var commonModule = loom.Module(", "loom.Provide(NewConfigLoader)", "func NewConfigLoader()"} {
		if !strings.Contains(string(cliOnly), want) {
			t.Errorf("CLI-only graph is missing %q:\n%s", want, cliOnly)
		}
	}
	for _, forbidden := range []string{"NewServer", "httpserver", "func InitApp", "type App struct"} {
		if strings.Contains(string(cliOnly), forbidden) {
			t.Errorf("CLI-only graph references %q; a non-HTTP project must not compile the server surface:\n%s", forbidden, cliOnly)
		}
	}
	httpOnly, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(http): %v", err)
	}
	if !strings.Contains(string(httpOnly), "NewServer") {
		t.Errorf("http-only graph has no server:\n%s", httpOnly)
	}
	if strings.Contains(string(httpOnly), "loom.Provide(NewPool)") || strings.Contains(string(httpOnly), "loom.Provide(NewAPIService)") {
		t.Errorf("http-only graph binds db/api:\n%s", httpOnly)
	}

	vars.Caps["db"] = true
	vars.Caps["api"] = true
	withDB, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(db,api): %v", err)
	}
	// Installing db declares the pool and repository as available bindings, but
	// the default composition never depends on them: the API service stays the
	// in-memory demo and the graph root holds no repository. The provider itself
	// lives in the stable api_provider.go seam, not in this regenerated graph.
	for _, want := range []string{"loom.Provide(NewPool)", "loom.As[data.Repository](NewRepository)", "loom.Provide(NewAPIService)"} {
		if !strings.Contains(string(withDB), want) {
			t.Errorf("db+api graph is missing %q:\n%s", want, withDB)
		}
	}
	if strings.Contains(string(withDB), "func NewAPIService") {
		t.Errorf("db+api graph defines NewAPIService in di.go; the provider must live in api_provider.go:\n%s", withDB)
	}
	for _, forbidden := range []string{"repositoryService", "NewAPIService(repo", "Repository data.Repository"} {
		if strings.Contains(string(withDB), forbidden) {
			t.Errorf("db+api graph still auto-wires persistence (%q):\n%s", forbidden, withDB)
		}
	}
	// ValidateDatabase guards the pool, the one constructor that needs it, and
	// not the graph root, so installing db does not require a credential to
	// serve.
	if count := strings.Count(string(withDB), "ValidateDatabase()"); count != 1 {
		t.Errorf("ValidateDatabase is called %d times in the db+api graph, want 1 (in NewPool):\n%s", count, withDB)
	}

	// Installing redis declares the client as an available binding, but the
	// default composition never depends on it: the provider lives in the stable
	// redis_provider.go seam, and the graph binds it only when a consumer asks
	// for *redis.Client.
	vars.Caps["redis"] = true
	withRedis, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(redis): %v", err)
	}
	if !strings.Contains(string(withRedis), "loom.Provide(NewRedisClient)") {
		t.Errorf("redis graph does not declare the Redis client binding:\n%s", withRedis)
	}
	if strings.Contains(string(withRedis), "func NewRedisClient") {
		t.Errorf("redis graph defines NewRedisClient in di.go; the provider must live in redis_provider.go:\n%s", withRedis)
	}

	// Installing mail and storage declares their bindings but the default graph
	// never depends on them, so Loom prunes them and the providers live in the
	// stable mail_provider.go and storage_provider.go seams.
	vars.Caps["mail"] = true
	vars.Caps["storage"] = true
	withAux, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(mail,storage): %v", err)
	}
	for _, want := range []string{"loom.Provide(NewMailSender)", "loom.Provide(NewObjectStore)"} {
		if !strings.Contains(string(withAux), want) {
			t.Errorf("mail+storage graph is missing %q:\n%s", want, withAux)
		}
	}
	for _, forbidden := range []string{"func NewMailSender", "func NewObjectStore"} {
		if strings.Contains(string(withAux), forbidden) {
			t.Errorf("mail+storage graph defines %s in di.go; the provider must live in its stable seam:\n%s", forbidden, withAux)
		}
	}

	// Installing cron is different: the graph root consumes the scheduler, so it
	// is constructed and its lifecycle hooks run with the server rather than
	// being pruned.
	vars.Caps["cron"] = true
	withCron, err := capability.RenderDIGraph(vars)
	if err != nil {
		t.Fatalf("RenderDIGraph(cron): %v", err)
	}
	for _, want := range []string{"loom.Provide(NewScheduler)", "Scheduler *cron.Scheduler", "example.com/demo/internal/cron"} {
		if !strings.Contains(string(withCron), want) {
			t.Errorf("cron graph is missing %q:\n%s", want, withCron)
		}
	}
	if strings.Contains(string(withCron), "func NewScheduler") {
		t.Errorf("cron graph defines NewScheduler in di.go; the provider must live in cron_provider.go:\n%s", withCron)
	}
}

// TestRenderDITestIsInMemoryAndHTTPOnly checks that the generated graph test
// exercises the in-memory API over a real socket and never injects a fake
// repository, and that the database is validated at the pool rather than the
// graph root.
func TestRenderDITestIsInMemoryAndHTTPOnly(t *testing.T) {
	capability, err := Load().Get("loom")
	if err != nil {
		t.Fatalf("Get loom: %v", err)
	}
	vars := DITemplateVars{
		Name:    "demo",
		Module:  "example.com/demo",
		Version: "0.1.0",
		Caps:    CapabilitySet{"base": true, "http": true, "loom": true, "db": true, "api": true},
	}
	test, err := capability.RenderDITest(vars)
	if err != nil {
		t.Fatalf("RenderDITest(db,api): %v", err)
	}
	for _, want := range []string{
		"TestComposedServerServesInMemoryAPI",
		"api.NewService",
		"TestNewConfigNeedsNoDatabase",
		"TestNewPoolRequiresDatabaseCredentials",
	} {
		if !strings.Contains(string(test), want) {
			t.Errorf("db+api graph test is missing %q:\n%s", want, test)
		}
	}
	// The regenerated test must not call the user-editable NewAPIService
	// provider: a user who changes its signature to consume data.Repository
	// would otherwise break the next regeneration with a compile error.
	if strings.Contains(string(test), "NewAPIService(") {
		t.Errorf("db+api graph test calls the editable NewAPIService provider:\n%s", test)
	}
	if strings.Contains(string(test), "fakeRepository") {
		t.Errorf("db+api graph test still injects a fake repository:\n%s", test)
	}
}
