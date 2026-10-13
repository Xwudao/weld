package weldtemplate

import (
	"bytes"
	"go/format"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// Static Go templates should be readable and formatted before rendering, not
// merely fixed up by the scaffolder's formatting pass.
func TestHTTPGoTemplatesAreFormatted(t *testing.T) {
	for _, file := range readDescriptor(t, "http").Files {
		if !strings.HasSuffix(file.Source, ".go.tmpl") {
			continue
		}
		source := path.Join("capabilities/http", file.Source)
		t.Run(source, func(t *testing.T) {
			raw, err := fs.ReadFile(FS(), source)
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := format.Source(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, formatted) {
				t.Error("Go template is not gofmt-formatted")
			}
		})
	}
}

func TestHTTPRuntimeLivesOutsideDI(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	di := read("capabilities/loom/files/di.go.tmpl")
	for _, forbidden := range []string{"type Server struct", "net.Listen(", "server.HTTP.Serve(", "server.HTTP.Shutdown("} {
		if strings.Contains(di, forbidden) {
			t.Errorf("DI retains HTTP runtime behavior: %s", forbidden)
		}
	}
	for _, required := range []string{"httpserver.NewHandler(", "httpserver.NewServer(", "OnStart: server.Start", "OnStop: server.Stop"} {
		if !strings.Contains(di, required) {
			t.Errorf("DI does not delegate to HTTP runtime: %s", required)
		}
	}
	server := read("capabilities/http/files/server.go.tmpl")
	if strings.Contains(server, "github.com/Xwudao/loom") {
		t.Error("HTTP runtime depends on Loom")
	}
}
