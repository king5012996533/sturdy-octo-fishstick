package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/hosted"

	"github.com/gin-gonic/gin"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dataDir := env("CANVAS_BACKEND_DATA_DIR", "data")
	autoMigrate, err := envBool("CANVAS_AUTO_MIGRATE", true)
	if err != nil {
		return err
	}
	corsMiddleware, err := cors()
	if err != nil {
		return err
	}
	workerTimeout, err := envDuration("CANVAS_SHUTDOWN_TIMEOUT", 10*time.Minute)
	if err != nil {
		return err
	}
	cookieSecure, err := envBool("CANVAS_AUTH_COOKIE_SECURE", false)
	if err != nil {
		return err
	}
	devEchoCode, err := envBool("CANVAS_AUTH_DEV_ECHO_CODE", false)
	if err != nil {
		return err
	}
	hostedAuth, err := envBool("CANVAS_HOSTED_AUTH", false)
	if err != nil {
		return err
	}
	factory, err := hostedFactory(hosted.Options{
		DatabaseDriver: env("CANVAS_AUTH_DATABASE_DRIVER", ""),
		DatabaseURL:    strings.TrimSpace(os.Getenv("CANVAS_AUTH_DATABASE_URL")),
		StateSecret:    strings.TrimSpace(os.Getenv("CANVAS_AUTH_STATE_SECRET")),
		CookieSecure:   cookieSecure,
		DevEchoCode:    devEchoCode,
	}, hostedAuth)
	if err != nil {
		return err
	}
	runtime, err := bootstrap.Open(ctx, bootstrap.Config{
		Profile:          bootstrap.ProfileServer,
		DataDir:          dataDir,
		DatabaseDriver:   env("CANVAS_DATABASE_DRIVER", "sqlite"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		ListenAddr:       env("CANVAS_BACKEND_ADDR", ":8080"),
		AutoMigrate:      autoMigrate,
		ShutdownTimeout:  workerTimeout,
		RouterMiddleware: []gin.HandlerFunc{corsMiddleware},
		HostedFactory:    factory,
	})
	if err != nil {
		return err
	}
	if err := runtime.Start(); err != nil {
		_ = runtime.Close(context.Background())
		return err
	}
	log.Printf("backend listening on %s", env("CANVAS_BACKEND_ADDR", ":8080"))

	var serveFailure error
	select {
	case <-ctx.Done():
	case err, ok := <-runtime.Errors():
		if ok && err != nil {
			serveFailure = err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), workerTimeout+30*time.Second)
	defer cancel()
	if err := errors.Join(serveFailure, runtime.Close(shutdownCtx)); err != nil {
		return err
	}
	log.Printf("backend stopped gracefully")
	return nil
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s 必须是 true 或 false", key)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s 必须是正数时长，例如 10m", key)
	}
	return parsed, nil
}

const corsAllowedHeaders = "Accept, Content-Type, Authorization, X-Requested-With, X-Canvas-Scene, X-Idempotency-Key, X-Canvas-Trace-ID, X-Canvas-Upstream-URL, X-Canvas-Upstream-Format, X-Canvas-Upstream-Base-URL"

const corsAllowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

type corsPolicy struct {
	origins  map[string]struct{}
	allowAny bool
}

func cors() (gin.HandlerFunc, error) {
	policy, err := parseCORSPolicy(os.Getenv("CANVAS_CORS_ORIGINS"))
	if err != nil {
		return nil, err
	}
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && !allowedOriginWithPolicy(c, origin, policy) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": http.StatusForbidden, "data": nil, "msg": "不允许的跨域来源"})
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		}
		c.Header("Access-Control-Allow-Headers", corsAllowedHeaders)
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-Canvas-Trace-ID, X-Diagnostic-Bundle-ID, X-Diagnostic-Schema-Version")
		c.Header("Access-Control-Allow-Methods", corsAllowedMethods)
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}, nil
}

func allowedOrigin(c *gin.Context, origin string) bool {
	policy, err := parseCORSPolicy(os.Getenv("CANVAS_CORS_ORIGINS"))
	if err != nil {
		return false
	}
	return allowedOriginWithPolicy(c, origin, policy)
}

func parseCORSPolicy(raw string) (corsPolicy, error) {
	policy := corsPolicy{origins: make(map[string]struct{})}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if value == "*" {
			policy.allowAny = true
			continue
		}
		normalized, err := normalizeCORSOrigin(value)
		if err != nil {
			return corsPolicy{}, fmt.Errorf("CANVAS_CORS_ORIGINS contains invalid origin %q: %w", value, err)
		}
		policy.origins[normalized] = struct{}{}
	}
	return policy, nil
}

func normalizeCORSOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("origin is empty")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("origin must be an http or https origin")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin must not contain a path, query, or fragment")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func allowedOriginWithPolicy(c *gin.Context, origin string, policy corsPolicy) bool {
	normalizedOrigin, err := normalizeCORSOrigin(origin)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(normalizedOrigin)
	if err != nil {
		return false
	}
	requestHost := c.Request.Host
	if forwardedHost := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); forwardedHost != "" {
		requestHost = strings.TrimSpace(strings.Split(forwardedHost, ",")[0])
	}
	if strings.EqualFold(parsed.Host, strings.TrimSpace(requestHost)) {
		return true
	}
	if policy.allowAny {
		return true
	}
	if _, ok := policy.origins[normalizedOrigin]; ok {
		return true
	}
	if len(policy.origins) > 0 {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return (host == "localhost" || host == "127.0.0.1" || host == "::1") && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// hostedFactory 按显式开关装配托管能力。
//
// 这里必须 fail-fast：托管形态下缺账号库会让服务退回单工作区模式，公网上就是一个
// 不需要登录、却能照常调用上游渠道的共享工作区。宁可起不来，也不能悄悄降级——所以
// "启用托管但没配账号库"是启动错误，而不是回退。
//
// 桌面版不读这个开关、也不注入工厂，认证代码因此不会进入桌面二进制。
func hostedFactory(options hosted.Options, enabled bool) (bootstrap.HostedFactory, error) {
	if !enabled {
		return nil, nil
	}
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return nil, errors.New("已启用托管登录（CANVAS_HOSTED_AUTH=true）但缺少 CANVAS_AUTH_DATABASE_URL：拒绝以无登录的单工作区模式启动")
	}
	return func(deps bootstrap.HostedDeps) (bootstrap.HostedExtension, error) {
		return hosted.New(deps, options)
	}, nil
}
