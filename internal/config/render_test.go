package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/primandproper/platform-go/v11/observability"
	"github.com/primandproper/platform-go/v11/observability/logging"
	loggingcfg "github.com/primandproper/platform-go/v11/observability/logging/config"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func validConfig(serviceName string) *Config {
	return &Config{
		Observability: observability.Config{
			Logging: loggingcfg.Config{
				Provider:    loggingcfg.ProviderSlog,
				ServiceName: serviceName,
				Level:       logging.InfoLevel,
			},
		},
	}
}

func TestRender(t *testing.T) {
	t.Parallel()

	t.Run("writes each config and round-trips through LoadFromFile", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		envs := []Environment{
			{Name: "localdev", Path: filepath.Join(dir, "localdev.json"), Config: validConfig("dev-service")},
			{Name: "production", Path: filepath.Join(dir, "nested", "production.json"), Config: validConfig("prod-service")},
		}

		must.NoError(t, Render(context.Background(), envs, true))

		for _, env := range envs {
			loaded, err := LoadFromFile(context.Background(), env.Path)
			must.NoError(t, err, must.Sprintf("loading %s", env.Name))
			test.Eq(t, env.Config.Observability.Logging.ServiceName, loaded.Observability.Logging.ServiceName)
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "config.json")
		env := []Environment{{Name: "localdev", Path: path, Config: validConfig("svc")}}

		must.NoError(t, Render(context.Background(), env, true))
		first, err := os.ReadFile(path)
		must.NoError(t, err)

		must.NoError(t, Render(context.Background(), env, true))
		second, err := os.ReadFile(path)
		must.NoError(t, err)

		test.Eq(t, first, second)
	})

	t.Run("validation rejects an invalid config before writing", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "config.json")
		// An unrecognized logging provider fails validation. (A zero Config does
		// not: the empty provider is the documented opt-out into noop logging.)
		broken := &Config{
			Observability: observability.Config{
				Logging: loggingcfg.Config{Provider: "nonsense"},
			},
		}
		env := []Environment{{Name: "broken", Path: path, Config: broken}}

		must.Error(t, Render(context.Background(), env, true))

		test.FileNotExists(t, path)
	})
}
