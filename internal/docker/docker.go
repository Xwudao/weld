// Package docker initializes user-owned Docker files and builds local images.
package docker

import (
	"encoding/json"
	"fmt"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Xwudao/weld/internal/build"
	"github.com/Xwudao/weld/internal/projectcfg"
	"golang.org/x/mod/modfile"
)

// metaMarker starts the generated capability metadata comment. It lets a later
// `weld build --docker` notice that the project's capabilities have changed and
// the user-owned Dockerfile no longer matches.
const metaMarker = "# weld:docker:generated"

// metadata is the parsed capability marker written by Files.
type metadata struct {
	present bool
	web     bool
	http    bool
}

// parseMetadata reads the capability marker from a Dockerfile. A Dockerfile
// without the marker (hand-written or generated before the marker existed) is
// reported as present=false.
func parseMetadata(raw []byte) metadata {
	var m metadata
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, metaMarker) {
			continue
		}
		m.present = true
		for _, field := range strings.Fields(strings.TrimPrefix(line, metaMarker)) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "web":
				m.web = value == "true"
			case "http":
				m.http = value == "true"
			}
		}
		break
	}
	return m
}

// Files renders Docker files without changing the project. Re-run init after
// adding web; existing files remain user-owned and are never silently replaced.
func Files(dir string, cfg *projectcfg.Config, web, http bool) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		return nil, err
	}
	if mod.Go == nil {
		return nil, fmt.Errorf("go.mod needs a go directive")
	}
	goVersion := mod.Go.Version
	if mod.Toolchain != nil && version.Compare(mod.Toolchain.Name, "go"+goVersion) > 0 {
		goVersion = strings.TrimPrefix(mod.Toolchain.Name, "go")
	}
	if !regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`).MatchString(goVersion) {
		return nil, fmt.Errorf("unsupported Go version %q", goVersion)
	}
	webEnabled := cfg.WebEnabled(web)
	var b strings.Builder
	// No `# syntax=` directive: the built-in frontend supports everything used
	// here, and pinning the external frontend image makes every build fetch it
	// from the registry first.
	fmt.Fprintf(&b, "%s web=%t http=%t\n# User-owned: regenerate explicitly after capability changes.\n", metaMarker, webEnabled, http)
	if webEnabled {
		frontend := cfg.EffectiveDev().Frontend
		clean := filepath.Clean(frontend.Dir)
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(clean, "\r\n\\") {
			return nil, fmt.Errorf("frontend directory must be a relative project subdirectory")
		}
		pm := frontend.Pm
		if pm != "pnpm" && pm != "npm" && pm != "yarn" {
			return nil, fmt.Errorf("unsupported frontend package manager %q", pm)
		}
		b.WriteString("FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web\nWORKDIR /src\nCOPY . .\n")
		fmt.Fprintf(&b, "WORKDIR %q\n", "/src/"+filepath.ToSlash(clean))
		switch pm {
		case "pnpm":
			b.WriteString("RUN npm install -g pnpm@12 && if [ -f pnpm-lock.yaml ]; then pnpm install --frozen-lockfile; else pnpm install; fi && pnpm run build\n")
		case "npm":
			b.WriteString("RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi && npm run build\n")
		case "yarn":
			b.WriteString("RUN corepack enable && yarn install && yarn run build\n")
		}
	}
	fmt.Fprintf(&b, "FROM --platform=$BUILDPLATFORM golang:%s-bookworm AS build\nWORKDIR /src\nCOPY go.mod go.sum ./\nRUN --mount=type=cache,target=/go/pkg/mod go mod download\nCOPY . .\n", goVersion)
	if webEnabled {
		b.WriteString("COPY --from=web /src/internal/web/dist ./internal/web/dist\n")
	}
	b.WriteString("ARG TARGETOS\nARG TARGETARCH\nARG TARGETVARIANT\nARG BUILD_TAGS\nARG BUILD_LDFLAGS\nARG CGO_ENABLED=0\nRUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \\\n    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} GOARM=${TARGETVARIANT#v} CGO_ENABLED=$CGO_ENABLED \\\n    go build -trimpath -tags \"$BUILD_TAGS\" -ldflags \"$BUILD_LDFLAGS\" -o /out/app .\n\nFROM gcr.io/distroless/static-debian12:nonroot\nWORKDIR /app\nCOPY --from=build /out/app /app/app\n")
	if http {
		b.WriteString("EXPOSE 8080\n")
	}
	b.WriteString("ENTRYPOINT [\"/app/app\"]\n")
	return map[string]string{"Dockerfile": b.String(), ".dockerignore": ignore}, nil
}

const ignore = `.git
.agents
.DS_Store
**/node_modules
bin
.env
.env.*
**/.env
**/.env.*
config.yml
**/config.yml
**/*.pem
**/*.key
**/*.crt
**/*.p12
.aws
# internal/web/dist is kept: Go embed needs its tracked placeholder.
`

// Init writes only absent files, using exclusive creation to protect edits.
func Init(dir string, files map[string]string, dry bool, out io.Writer) error {
	for _, name := range []string{"Dockerfile", ".dockerignore"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			fmt.Fprintf(out, "kept %s (user-owned)\n", path)
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if dry {
			fmt.Fprintf(out, "would add %s\n", path)
			continue
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(files[name])
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(out, "added %s\n", path)
	}
	return nil
}

// Request describes a local Docker image build. Overrides take precedence over weld.yml.
type Request struct {
	Dir, Name      string
	Config         *projectcfg.Config
	Overrides      projectcfg.DockerConfig
	Runner         build.Runner
	Stdout, Stderr io.Writer
	DryRun         bool
	// WebInstalled and HTTPInstalled report the installed capabilities. They
	// let Run warn when the user-owned Dockerfile was generated for a
	// different set.
	WebInstalled  bool
	HTTPInstalled bool
}

func Run(req Request) (string, error) {
	cfg := projectcfg.DockerConfig{}
	if req.Config != nil {
		cfg = req.Config.Build.Docker
	}
	if req.Overrides.Image != "" {
		cfg.Image = req.Overrides.Image
	}
	if req.Overrides.Dockerfile != "" {
		cfg.Dockerfile = req.Overrides.Dockerfile
	}
	if req.Overrides.Platform != "" {
		cfg.Platform = req.Overrides.Platform
	}
	if cfg.Image == "" {
		name := req.Name
		if name == "" {
			abs, err := filepath.Abs(req.Dir)
			if err != nil {
				return "", err
			}
			name = filepath.Base(abs)
		}
		cfg.Image = strings.ToLower(name) + ":latest"
	}
	if cfg.Dockerfile == "" {
		cfg.Dockerfile = "Dockerfile"
	}
	if cfg.Platform == "" {
		cfg.Platform = "linux/amd64"
	}
	if !regexp.MustCompile(`^linux/(amd64|arm64(/v8)?|arm/v[5-7])$`).MatchString(cfg.Platform) {
		return "", fmt.Errorf("unsupported Docker platform %q (one Linux platform required)", cfg.Platform)
	}
	path := cfg.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(req.Dir, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Dockerfile not found: %s (run: weld docker init)", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if meta := parseMetadata(raw); meta.present && req.Stderr != nil {
		wantWeb := req.Config.WebEnabled(req.WebInstalled)
		if meta.web != wantWeb || meta.http != req.HTTPInstalled {
			fmt.Fprintf(req.Stderr, "warning: %s was generated for web=%t http=%t but the project now has web=%t http=%t; re-run weld docker init or update it\n", cfg.Dockerfile, meta.web, meta.http, wantWeb, req.HTTPInstalled)
		}
	}
	args := []string{"build", "--load", "--file", cfg.Dockerfile, "--tag", cfg.Image, "--platform", cfg.Platform, "--build-arg", "BUILD_TAGS=" + req.Config.BuildTags(), "--build-arg", "BUILD_LDFLAGS=" + req.Config.BuildLdflags()}
	cgo := "0"
	if req.Config != nil && req.Config.Build.Cgo != nil && *req.Config.Build.Cgo {
		cgo = "1"
		if strings.Contains(string(raw), "FROM gcr.io/distroless/static-debian12:nonroot") {
			return "", fmt.Errorf("CGO_ENABLED=1 requires a custom Dockerfile with a compatible runtime (default image is static)")
		}
	}
	args = append(args, "--build-arg", "CGO_ENABLED="+cgo, ".")
	if req.DryRun {
		encoded, _ := json.Marshal(args)
		fmt.Fprintf(req.Stdout, "docker %s\n", encoded)
		return cfg.Image, nil
	}
	runner := req.Runner
	if runner == nil {
		runner = func(name, dir string, env []string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Dir = dir
			cmd.Stdout = req.Stdout
			cmd.Stderr = req.Stderr
			return cmd.Run()
		}
	}
	if err := runner("docker", req.Dir, nil, args...); err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}
	return cfg.Image, nil
}
