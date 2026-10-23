package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xwudao/weld/internal/project"
)

func TestUpgradeLoomAddsBusinessWiringSeams(t *testing.T) {
	t.Parallel()
	loomToolAvailable(t)
	dir := create(t, t.TempDir())
	add(t, dir, "http")
	for _, path := range []string{"internal/di/http_routes.go", "internal/di/app_provider.go"} {
		if err := os.Remove(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
		forget(t, dir, path)
	}
	// Model the previous graph: NewServer had no project routes or startup root.
	const path = "internal/di/di.go"
	graph := readFile(t, filepath.Join(dir, path))
	for _, fragment := range []string{
		"\tloom.Provide(NewProjectRoutes),\n",
		"\tloom.Provide(NewAppDependencies),\n",
		"\troutes ProjectRoutes,\n",
		"\t_ *AppDependencies,\n",
		"\t\troutes...,\n",
	} {
		if !strings.Contains(graph, fragment) {
			t.Fatalf("fixture graph missing %q", fragment)
		}
		graph = strings.Replace(graph, fragment, "", 1)
	}
	weldWrote(t, dir, "loom", path, graph)
	downgrade(t, dir, "loom", "0.7.2")
	result := upgrade(t, dir, []string{"loom"}, false)
	for _, path := range []string{"internal/di/http_routes.go", "internal/di/app_provider.go", "internal/di/di.go"} {
		plannedOperation(t, result.Result, path)
	}
	if err := result.Apply(); err != nil {
		t.Fatal(err)
	}
	manifest := mustLoad(t, dir)
	for _, path := range []string{"internal/di/http_routes.go", "internal/di/app_provider.go"} {
		found := false
		for _, entry := range manifest.Files {
			if entry.Path == path {
				found = entry.Role == project.RoleSeam
			}
		}
		if !found {
			t.Fatalf("%s not recorded as project-owned seam", path)
		}
	}
	if !buildAndTestGeneratedProject(t, dir) {
		t.Skip("cannot resolve generated project dependencies")
	}
}
