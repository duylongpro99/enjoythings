package devtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevModeHasParameterizedWatcherEntrypoint(t *testing.T) {
	root := repoRoot(t)

	makefile := readText(t, filepath.Join(root, "Makefile"))
	requireContains(t, makefile, "SERVICE ?= api")
	requireContains(t, makefile, "DEV_CMD=go run ./cmd/$(SERVICE)")
	requireContains(t, makefile, "air -c .air.toml")
	requireContains(t, makefile, "docker compose -f docker-compose.dev.yml up -d postgres")

	airConfig := readText(t, filepath.Join(root, ".air.toml"))
	requireContains(t, airConfig, `cmd = "${DEV_CMD}"`)
	requireContains(t, airConfig, `include_dir = ["cmd", "internal", "db"]`)
	requireContains(t, airConfig, `exclude_dir = ["tmp", "vendor"]`)
}

func TestDevComposeDoesNotBuildGoServiceContainer(t *testing.T) {
	root := repoRoot(t)

	devCompose := readText(t, filepath.Join(root, "docker-compose.dev.yml"))
	requireContains(t, devCompose, "services:")
	requireContains(t, devCompose, "postgres:")

	if strings.Contains(devCompose, "\n  api:") {
		t.Fatal("docker-compose.dev.yml must not define the API container; dev mode runs Go services locally")
	}
	if strings.Contains(devCompose, "build:") {
		t.Fatal("docker-compose.dev.yml must not build containers for Go services")
	}
}

func TestReadmeDocumentsWatchModeForAnyGoService(t *testing.T) {
	root := repoRoot(t)

	readme := readText(t, filepath.Join(root, "README.md"))
	requireContains(t, readme, "make dev")
	requireContains(t, readme, "SERVICE=api make dev")
	requireContains(t, readme, "SERVICE=<name> make dev")
	requireContains(t, readme, "go install github.com/air-verse/air@latest")
	requireContains(t, readme, "docker compose up --build")
}

func TestWalletRepositoryUsesGeneratedQueries(t *testing.T) {
	root := repoRoot(t)

	walletRepo := readText(t, filepath.Join(root, "internal", "repo", "wallets.go"))
	requireContains(t, walletRepo, `"enjoythings/services/internal/repo/queries"`)
	requireContains(t, walletRepo, "db.queries.CreateWallet")
	requireContains(t, walletRepo, "db.queries.GetWallet")

	for _, rawSQL := range []string{
		"INSERT INTO wallets",
		"SELECT id, user_id, balance, currency, created_at, updated_at",
	} {
		if strings.Contains(walletRepo, rawSQL) {
			t.Fatalf("wallet repository must use sqlc-generated queries, found raw SQL fragment %q", rawSQL)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	return filepath.Dir(wd)
}

func readText(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func requireContains(t *testing.T, text string, want string) {
	t.Helper()

	if !strings.Contains(text, want) {
		t.Fatalf("expected text to contain %q", want)
	}
}
