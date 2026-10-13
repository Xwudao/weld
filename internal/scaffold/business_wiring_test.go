package scaffold

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise service injection into policies and jobs with the real generated
// graph. The module imports httpserver, so putting policy wiring in that package
// would introduce an import cycle. Custom providers are declared only once.
func TestBusinessPoliciesAndCronJobs(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	addModule(t, dir, "widget")
	add(t, dir, "cron")
	for _, path := range []string{
		"internal/di/repository_providers.go",
		"internal/cron/deps.go",
		"internal/cron/register.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Fatalf("generated project retains removed wiring entry %s: %v", path, err)
		}
	}
	mod := strings.Fields(readFile(t, filepath.Join(dir, "go.mod")))[1]
	putGo := func(path, body string) string {
		t.Helper()
		formatted, err := format.Source([]byte(strings.ReplaceAll(body, "__module__", mod)))
		if err != nil {
			t.Fatal(err)
		}
		edited(t, dir, path, string(formatted))
		return string(formatted)
	}
	providers := putGo("internal/di/providers.go", `package di

import (
    "context"
    "__module__/internal/modules/widget"
    "github.com/Xwudao/loom"
)

var projectModule = loom.Module(loom.Provide(NewReporter))

type Reporter struct {
    service widget.Service
    ran chan string
}

var reporter *Reporter

func NewReporter(service widget.Service) *Reporter {
    reporter = &Reporter{service: service, ran: make(chan string, 8)}
    return reporter
}

func (r *Reporter) Generate(ctx context.Context) error {
    response, err := r.service.Greet(ctx, widget.Request{Name: "cron"})
    if err != nil { return err }
    select {
    case r.ran <- response.Message:
    default:
    }
    return nil
}
`)
	policies := putGo("internal/di/http_policies.go", `package di

import (
    "net/http"
    "__module__/internal/httpserver"
    "__module__/internal/modules/widget"
)

var policyError error

func NewHTTPPolicies(service widget.Service) ([]httpserver.NamedPolicy, error) {
    if policyError != nil { return nil, policyError }
    return []httpserver.NamedPolicy{{
        Name: httpserver.PolicyAdmin,
        Middleware: func(next http.Handler) http.Handler {
            return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                response, err := service.Greet(r.Context(), widget.Request{Name: "policy"})
                if err != nil { http.Error(w, err.Error(), 500); return }
                w.Header().Set("X-Policy-Greeting", response.Message)
                next.ServeHTTP(w, r)
            })
        },
    }}, nil
}
`)
	jobs := putGo("internal/di/cron_jobs.go", `package di

import (
    "time"
    "__module__/internal/cron"
)

type CronJobs struct{}

func NewCronJobs(s *cron.Scheduler, report *Reporter) (*CronJobs, error) {
    if err := s.Register("report", cron.Every(time.Second), report.Generate); err != nil {
        return nil, err
    }
    return &CronJobs{}, nil
}
`)
	// Regenerate after signature changes, and prove stable business wiring stays
	// intact when unrelated capabilities are added.
	add(t, dir, "redis")
	for path, want := range map[string]string{
		"internal/di/providers.go":     providers,
		"internal/di/http_policies.go": policies,
		"internal/di/cron_jobs.go":     jobs,
	} {
		if got := readFile(t, filepath.Join(dir, path)); got != want {
			t.Fatalf("later add rewrote %s", path)
		}
	}
	gen := readFile(t, filepath.Join(dir, "internal/di/loom_gen.go"))
	for _, want := range []string{"NewReporter(", "NewHTTPPolicies(", "NewCronJobs("} {
		if !strings.Contains(gen, want) {
			t.Fatalf("graph pruned %s", want)
		}
	}
	putGo("internal/di/business_wiring_test.go", `package di

import (
    "context"
    "errors"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

func wiringContext() context.Context {
    return WithAddr(WithConfigPath(context.Background(), "../../config.yml"), "127.0.0.1:0")
}

func TestPolicyUsesInjectedBusinessService(t *testing.T) {
    app, _, err := InitApp(wiringContext())
    if err != nil { t.Fatal(err) }
    w := httptest.NewRecorder()
    request := httptest.NewRequest(http.MethodPost, "/api/widget/admin/items", strings.NewReader("{\"name\":\"demo\"}"))
    request.Header.Set("Content-Type", "application/json")
    app.Server.HTTP.Handler.ServeHTTP(w, request)
    if w.Code >= 400 || !strings.Contains(w.Header().Get("X-Policy-Greeting"), "policy") {
        t.Fatalf("business policy was not applied: %d %s", w.Code, w.Body.String())
    }
}

func TestCronMethodRunsAfterLifecycleStart(t *testing.T) {
    _, lifecycle, err := InitApp(wiringContext())
    if err != nil { t.Fatal(err) }
    select {
    case <-reporter.ran: t.Fatal("job ran during graph construction")
    default:
    }
    if err := lifecycle.Start(context.Background()); err != nil { t.Fatal(err) }
    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        if err := lifecycle.Stop(ctx); err != nil { t.Error(err) }
    }()
    select {
    case got := <-reporter.ran:
        if !strings.Contains(got, "cron") { t.Fatalf("got %q", got) }
    case <-time.After(5*time.Second):
        t.Fatal("registered business method never ran")
    }
}

func TestPolicyErrorFailsConstruction(t *testing.T) {
    want := errors.New("policy configuration invalid")
    policyError = want
    defer func() { policyError = nil }()
    app, _, err := InitApp(wiringContext())
    if !errors.Is(err, want) || app != nil {
        t.Fatalf("InitApp = %v, %v; want policy error", app, err)
    }
}
`)
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project dependencies")
	}
	// The generated scheduler's unit tests cover error logging, panic recovery,
	// overlap and cancellation; also exercise the new injected seams under race.
	runGo(t, dir, "test", "-race", "./internal/di", "./internal/cron")
}
