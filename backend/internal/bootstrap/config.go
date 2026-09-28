package bootstrap

import (
	"time"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

type Profile string

const (
	ProfileServer  Profile = "server"
	ProfileDesktop Profile = "desktop"
)

type Config struct {
	Profile          Profile
	DataDir          string
	DatabaseDriver   string
	DatabaseURL      string
	ListenAddr       string
	LaunchToken      string
	AutoMigrate      bool
	ShutdownTimeout  time.Duration
	RouterMiddleware []gin.HandlerFunc

	// HostedFactory 由服务端形态注入托管能力（目前是登录模块）。
	//
	// 这里用工厂而不是直接引用实现包，是为了让本地桌面二进制不链接任何托管
	// 基础设施：见 desktop_dependencies_test.go 的依赖边界约束。
	HostedFactory HostedFactory
}

// HostedDeps 是装配托管能力所需的宿主依赖。
type HostedDeps struct {
	DataDir string
	Service *app.Service
}

// HostedExtension 是托管能力的生命周期接口。
//
// WorkspaceMiddleware 取代桌面版的固定工作区中间件：它负责把请求映射到具体账号，
// 因此必须由托管侧提供，而不是在 bootstrap 里写死。
type HostedExtension interface {
	WorkspaceMiddleware() gin.HandlerFunc
	RegisterRoutes(api *gin.RouterGroup)
	Close() error
}

// HostedFactory 在运行时装配托管能力；返回 nil 表示该实例不启用。
type HostedFactory func(deps HostedDeps) (HostedExtension, error)

func (c Config) withDefaults() Config {
	if c.Profile == "" {
		c.Profile = ProfileServer
	}
	if c.DatabaseDriver == "" {
		c.DatabaseDriver = "sqlite"
	}
	if c.ListenAddr == "" {
		if c.Profile == ProfileDesktop {
			c.ListenAddr = "127.0.0.1:0"
		} else {
			c.ListenAddr = ":8080"
		}
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 10 * time.Minute
	}
	return c
}
