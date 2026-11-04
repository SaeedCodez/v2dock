// Command v2dock is one binary with three daemons (agent, web, guard) and
// the CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // quota periods follow TZ (e.g. Asia/Tehran) without OS tzdata

	"github.com/SaeedCodez/v2dock/internal/agent"
	"github.com/SaeedCodez/v2dock/internal/cli"
	"github.com/SaeedCodez/v2dock/internal/docker"
	"github.com/SaeedCodez/v2dock/internal/web"
	"github.com/SaeedCodez/v2dock/internal/wire"
)

var version = "dev"

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func setupLogging() {
	level := slog.LevelInfo
	if env("V2DOCK_LOG_LEVEL", "info") == "debug" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func main() {
	agent.Version = version
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "agent", "web", "guard":
			setupLogging()
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := runDaemon(ctx, os.Args[1]); err != nil {
				slog.Error(os.Args[1]+" failed", "err", err)
				os.Exit(1)
			}
			return
		case "version", "--version":
			fmt.Println("v2dock", version)
			return
		case "healthcheck":
			// Used by the web container's HEALTHCHECK (the image has no curl).
			url := "http://127.0.0.1:8080/healthz"
			if len(os.Args) > 2 {
				url = os.Args[2]
			}
			c := &http.Client{Timeout: 5 * time.Second}
			resp, err := c.Get(url)
			if err != nil || resp.StatusCode != http.StatusOK {
				os.Exit(1)
			}
			return
		}
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

func runDaemon(ctx context.Context, which string) error {
	switch which {
	case "agent":
		// Read secrets, then drop them from the environment so child
		// processes and /proc/<pid>/environ readers do not see them.
		cfg := agent.Config{
			DataDir:       env("V2DOCK_DATA", "/data"),
			SocketPath:    env("V2DOCK_SOCKET", wire.DefaultAgentSocket),
			SocketGID:     envInt("V2DOCK_SOCKET_GID", 65532),
			DockerSocket:  env("V2DOCK_DOCKER_SOCKET", wire.DefaultGuardSocket),
			AssetDir:      env("V2DOCK_ASSETS", "/usr/share/v2dock"),
			TProxyPort:    envInt("V2DOCK_TPROXY_PORT", 12701),
			SetupToken:    os.Getenv("V2DOCK_SETUP_TOKEN"),
			AdminPassword: firstEnv("V2DOCK_ADMIN_PASSWORD", "SERVICE_PASSWORD_ADMIN"),
			NoFirewall:    env("V2DOCK_NO_FIREWALL", "") == "1",
		}
		os.Unsetenv("V2DOCK_ADMIN_PASSWORD")
		os.Unsetenv("SERVICE_PASSWORD_ADMIN")
		os.Unsetenv("V2DOCK_SETUP_TOKEN")
		a, err := agent.New(cfg)
		if err != nil {
			return err
		}
		return a.Run(ctx)
	case "web":
		cfg := web.Config{
			Listen:         env("V2DOCK_LISTEN", ":8080"),
			AgentSocket:    env("V2DOCK_SOCKET", wire.DefaultAgentSocket),
			TrustedProxies: web.DefaultTrustedProxies(),
		}
		if tp := os.Getenv("V2DOCK_TRUSTED_PROXIES"); tp != "" {
			cfg.TrustedProxies = nil
			for _, s := range strings.Split(tp, ",") {
				p, err := netip.ParsePrefix(strings.TrimSpace(s))
				if err != nil {
					return fmt.Errorf("V2DOCK_TRUSTED_PROXIES: %w", err)
				}
				cfg.TrustedProxies = append(cfg.TrustedProxies, p)
			}
		}
		return web.Serve(ctx, cfg)
	case "guard":
		return docker.RunGuard(ctx,
			env("V2DOCK_DOCKER_HOST_SOCKET", "/var/run/docker.sock"),
			env("V2DOCK_GUARD_SOCKET", wire.DefaultGuardSocket),
			0)
	}
	return nil
}
