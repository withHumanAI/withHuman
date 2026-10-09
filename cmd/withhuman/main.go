// withhuman runs the entire provider in one process: embedded PostgreSQL,
// embedded Temporal, the approval worker, and the HTTP server with the
// bundled frontend.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/withHumanAI/withHuman/loggermdc"
	"github.com/withHumanAI/withHuman/runtime"
	"github.com/withHumanAI/withHuman/singlebinary"
	"github.com/withHumanAI/withHuman/webapp"
)

var version = "dev"

func main() {
	logger := loggermdc.Configure(slog.NewJSONHandler(os.Stdout, nil))
	dataDir := flag.String("data-dir", "", "directory for all persistent state (default $WITHHUMAN_DATA_DIR or ~/.withhuman)")
	httpAddress := flag.String("http-address", "", "public listen address (default $WITHHUMAN_HTTP_ADDRESS or :8080)")
	flag.Parse()

	if err := run(logger, *dataDir, *httpAddress); err != nil {
		logger.Error("withhuman stopped", "error", err)
		os.Exit(1)
	}
}

// The open edition in one process: no lifecycle hooks, no extra routes,
// unlimited entitlements, and the bootstrap ceremony as the only
// organization-creation path.
func run(logger *slog.Logger, dataDir, httpAddress string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	instance, err := singlebinary.Start(ctx, singlebinary.Config{
		DataDir:     dataDir,
		HTTPAddress: httpAddress,
		Logger:      logger,
		Version:     version,
		Server:      runtime.ServerOptions{Frontend: webapp.Assets()},
	})
	if err != nil {
		return err
	}
	if instance.BootstrapSecretPath != "" {
		logger.Info("bootstrap secret ready; the first owner account is created with it",
			"path", instance.BootstrapSecretPath)
	}

	select {
	case <-ctx.Done():
	case err := <-instance.ServeErrors():
		if err != nil {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = instance.Shutdown(shutdownContext)
			return err
		}
	}
	logger.Info("shutting down")
	shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return instance.Shutdown(shutdownContext)
}
