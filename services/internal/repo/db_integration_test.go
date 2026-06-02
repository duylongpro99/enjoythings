package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresContainerCanBePingedThroughPool(t *testing.T) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "enjoythings_test",
			"POSTGRES_USER":     "enjoythings",
			"POSTGRES_PASSWORD": "enjoythings_test_password",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("Postgres container unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate Postgres container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	databaseURL := fmt.Sprintf(
		"postgres://enjoythings:enjoythings_test_password@%s:%s/enjoythings_test?sslmode=disable",
		host,
		port.Port(),
	)
	db, err := Connect(ctx, databaseURL, 2)
	if err != nil {
		t.Fatalf("connect to Postgres container: %v", err)
	}
	t.Cleanup(db.Close)

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("ping Postgres container: %v", err)
	}
}
