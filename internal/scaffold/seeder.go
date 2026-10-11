package scaffold

import (
	"fmt"
	"go/token"
	"path/filepath"

	"github.com/Xwudao/weld/internal/project"
	"github.com/Xwudao/weld/internal/template"
)

const (
	// seederRegisterMarker is the extension point in register.go that
	// `weld add seeder <name>` appends a registration to.
	seederRegisterMarker = "seeders"
	// seederDir is the seeder package, relative to the project root.
	seederDir = "internal/data/seeder"
)

// ValidateSeederName reports why a seeder name is unusable. The name is both the
// seed_<name>.go file suffix and the seed<Name> function, so it must be a valid,
// lower-case Go identifier.
func ValidateSeederName(name string) error {
	if !moduleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid seeder name %q: use lowercase letters and digits starting with a letter (a valid Go identifier)", name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("invalid seeder name %q: it is a Go keyword", name)
	}
	return nil
}

// AddSeeder plans adding a new seeder named name to the seed capability. It
// writes internal/data/seeder/seed_<name>.go and appends the registration to the
// stable register.go index, so the run order stays readable in one place and the
// seeder body is its own file.
//
// The seed capability (and its db/loom/config chain) must be installed first; a
// missing requirement is an error unless Request.WithDependencies is set, in
// which case the chain and the seeder are planned together.
func AddSeeder(req Request, name string) (*Result, error) {
	if err := ValidateSeederName(name); err != nil {
		return nil, err
	}
	manifest, err := project.Load(req.Dir)
	if err != nil {
		return nil, err
	}
	seedCapability, err := req.Catalog.Get("seed")
	if err != nil {
		return nil, err
	}

	path := filepath.Join(seederDir, "seed_"+name+".go")
	result := &Result{Dir: req.Dir}
	// The seeder file is recorded under seed:<name>, so an owned file means the
	// seeder already exists and a repeat add is a no-op. An unowned file with the
	// same path is still a conflict through planNewFile.
	if manifest.Owns(path) {
		result.Notes = []string{fmt.Sprintf("seeder %s is already installed", name)}
		return result, nil
	}

	p := newPlanner(req, manifest, result)
	if !manifest.HasCapability("seed") {
		order, err := requiredCapabilities(req.Catalog, manifest, seedCapability)
		if err != nil {
			return nil, err
		}
		if !req.WithDependencies {
			return nil, &MissingRequirementsError{Subject: fmt.Sprintf("seeder %q", name), Missing: capabilityNames(order)}
		}
		for _, capability := range order {
			result.Notes = append(result.Notes, fmt.Sprintf("installing required capability %q", capability.Name))
			if err := p.installCapability(capability); err != nil {
				return nil, err
			}
		}
	}

	if err := p.planNewFile(path, "seed:"+name, seederFile(name)); err != nil {
		return nil, err
	}
	registration := fmt.Sprintf("\t// weld:seed:%s:installed %s\n\tr.Register(%q, seed%s)\n",
		name, seedCapability.Version, name, template.ExportName(name))
	if err := p.planMarker(filepath.Join(seederDir, "register.go"), seederRegisterMarker, "seed:"+name, "seed", []byte(registration)); err != nil {
		return nil, err
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	return result, nil
}

// seederFile renders the seed_<name>.go stub. It is generated in code rather
// than shipped as a payload because it has no capability-dependent shape: it is
// the same file for every name but the identifiers.
func seederFile(name string) []byte {
	exported := template.ExportName(name)
	return []byte(fmt.Sprintf(`package seeder

import "context"

// seed%s is the %s seeder. It is registered in register.go and runs in the order
// listed there. It must be idempotent: seed keeps no ledger, so running it twice
// re-runs it.
//
// Write through the repositories on Deps (see seeder.go). Add a field to Deps
// and wire it in internal/di/seed_providers.go when you need a repository the
// shipped Items does not cover. For generated fake data, use a third-party
// library such as github.com/brianvoe/gofakeit/v7 — weld ships no faker
// dependency.
func seed%s(ctx context.Context, deps *Deps) error {
	_ = ctx
	_ = deps
	return nil
}
`, exported, name, exported))
}
