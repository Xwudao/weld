package db

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrTestDatabaseNotConfigured reports that no test database is configured: no
// PG_TEST_DSN, no database_test.dsn, and no database_test.name.
var ErrTestDatabaseNotConfigured = errors.New("weld db: test database is not configured")

// Test database environment names, matching the generated internal/testdb
// helper so `weld test` and the generated tests agree.
const (
	EnvTestDatabaseURL    = "PG_TEST_DSN"
	EnvTestDatabaseURLAlt = "TEST_DATABASE_URL"
	EnvTestDBHost         = "DB_TEST_HOST"
	EnvTestDBPort         = "DB_TEST_PORT"
	EnvTestDBUser         = "DB_TEST_USER"
	EnvTestDBPassword     = "DB_TEST_PASSWORD"
	EnvTestDBName         = "DB_TEST_NAME"
)

// ResolveTestDSN returns the PostgreSQL dsn the generated integration tests
// use.
//
// Precedence matches the generated internal/testdb resolver: PG_TEST_DSN (or
// TEST_DATABASE_URL), then database_test.dsn, then database_test.name with the
// application connection fields. It never falls back to the application dsn, so
// `weld test` cannot point the tests at a live database by accident. The
// returned source is safe to print; the dsn is not.
func ResolveTestDSN(dir, configPath string, lookupEnv func(string) (string, bool)) (dsn, source string, err error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if value, ok := lookupEnv(EnvTestDatabaseURL); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, EnvTestDatabaseURL, nil
		}
	}
	if value, ok := lookupEnv(EnvTestDatabaseURLAlt); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, EnvTestDatabaseURLAlt, nil
		}
	}

	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.yml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", ErrTestDatabaseNotConfigured
		}
		return "", "", fmt.Errorf("weld db: read %s: %w", path, err)
	}
	var file struct {
		Environment string `yaml:"environment"`
		Database    struct {
			Host     string `yaml:"host"`
			Port     int    `yaml:"port"`
			User     string `yaml:"user"`
			Password string `yaml:"password"`
			Name     string `yaml:"name"`
			DSN      string `yaml:"dsn"`
		} `yaml:"database"`
		DatabaseTest struct {
			Host       string `yaml:"host"`
			Port       int    `yaml:"port"`
			User       string `yaml:"user"`
			Password   string `yaml:"password"`
			Name       string `yaml:"name"`
			DSN        string `yaml:"dsn"`
			SchemaOnly bool   `yaml:"schema_only"`
		} `yaml:"database_test"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		// The YAML error can quote the offending line, which may hold a password.
		return "", "", fmt.Errorf("weld db: %s: invalid YAML", path)
	}

	test := &file.DatabaseTest
	if value, ok := lookupEnv(EnvTestDBHost); ok {
		test.Host = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvTestDBPort); ok {
		port, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil {
			return "", "", fmt.Errorf("weld db: %s is not a number", EnvTestDBPort)
		}
		test.Port = port
	}
	if value, ok := lookupEnv(EnvTestDBUser); ok {
		test.User = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvTestDBPassword); ok {
		test.Password = value
	}
	if value, ok := lookupEnv(EnvTestDBName); ok {
		test.Name = strings.TrimSpace(value)
	}

	if explicit := strings.TrimSpace(test.DSN); explicit != "" {
		return explicit, path, nil
	}
	if test.SchemaOnly {
		environment := file.Environment
		if value, ok := lookupEnv("APP_ENV"); ok {
			environment = strings.TrimSpace(value)
		}
		if !isLocalEnvironment(environment) {
			return "", "", errors.New("weld db: database_test.schema_only requires environment local, development or test")
		}
		appDSN, _, appErr := ResolveDSN(dir, configPath, lookupEnv)
		if appErr != nil {
			return "", "", errors.New("weld db: database_test.schema_only needs a configured application database")
		}
		return appDSN, path, nil
	}
	name := strings.TrimSpace(test.Name)
	if name == "" {
		return "", "", ErrTestDatabaseNotConfigured
	}
	// The inherited application fields must see the same DB_* overrides as the
	// generated config loader, otherwise weld test and testdb resolve different
	// targets when a deployment sets them.
	app := file.Database
	if value, ok := lookupEnv(EnvDBHost); ok {
		app.Host = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvDBPort); ok {
		port, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil {
			return "", "", fmt.Errorf("weld db: %s is not a number", EnvDBPort)
		}
		app.Port = port
	}
	if value, ok := lookupEnv(EnvDBUser); ok {
		app.User = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv(EnvDBPassword); ok {
		app.Password = value
	}
	if value, ok := lookupEnv(EnvDBName); ok {
		app.Name = strings.TrimSpace(value)
	}
	host := firstNonBlankString(test.Host, app.Host)
	port := test.Port
	if port == 0 {
		port = app.Port
	}
	user := firstNonBlankString(test.User, app.User)
	password := test.Password
	if password == "" {
		password = app.Password
	}
	if host == "" {
		host = DefaultDBHost
	}
	if port == 0 {
		port = DefaultDBPort
	}
	switch {
	case port <= 0:
		return "", "", errors.New("weld db: database_test.port must be positive")
	case user == "":
		return "", "", fmt.Errorf("weld db: database_test.name is set but database.user is empty (set it in %s, %s or %s)", path, EnvTestDBUser, EnvTestDatabaseURL)
	}
	return (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   name,
	}).String(), path, nil
}

// isLocalEnvironment reports whether the configured environment allows the
// same-database schema_only mode.
func isLocalEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "local", "development", "test":
		return true
	default:
		return false
	}
}

// firstNonBlankString returns the first value that is not blank after trimming.
func firstNonBlankString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
