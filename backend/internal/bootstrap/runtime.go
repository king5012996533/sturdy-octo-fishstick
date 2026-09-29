package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"infinite-canvas/backend/internal/app"
	localasset "infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/beefapi"
	"infinite-canvas/backend/internal/buildinfo"
	"infinite-canvas/backend/internal/database"
	canvasHandler "infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/localapp"
	localproject "infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/repository"
	localtask "infinite-canvas/backend/internal/task"
	httptransport "infinite-canvas/backend/internal/transport/http"
	"infinite-canvas/backend/internal/workspace"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Runtime struct {
	cfg         Config
	db          *gorm.DB
	service     *app.Service
	localApp    *localapp.App
	handler     http.Handler
	status      *systemStatus
	launchToken string
	beefAPI     *beefapi.Service
	hosted      HostedExtension
	listener    net.Listener
	httpServer  *http.Server
	serveErr    chan error
	started     atomic.Bool
	closed      atomic.Bool
	closeOnce   sync.Once
	background  sync.WaitGroup
	closeErr    error
}

func Open(_ context.Context, raw Config) (*Runtime, error) {
	cfg := raw.withDefaults()
	if cfg.Profile != ProfileServer && cfg.Profile != ProfileDesktop {
		return nil, fmt.Errorf("不支持的运行模式：%s", cfg.Profile)
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("后端数据目录不能为空")
	}
	if cfg.Profile == ProfileDesktop && !strings.HasPrefix(cfg.ListenAddr, "127.0.0.1:") {
		return nil, errors.New("桌面运行时只能监听 127.0.0.1")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := database.Open(database.Config{Driver: cfg.DatabaseDriver, DSN: cfg.DatabaseURL, DataDir: cfg.DataDir})
	if err != nil {
		return nil, err
	}
	cleanupDB := func() {
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	if err := database.ConfigurePool(db); err != nil {
		cleanupDB()
		return nil, err
	}
	if cfg.AutoMigrate {
		err = database.MigrateLocalSchema(db)
	} else {
		err = database.RequireLocalSchema(db)
	}
	if err == nil && cfg.HostedFactory != nil {
		// 审计流水是托管专属表，本地结构迁移刻意不含它（桌面产物必须证明结构里
		// 没有托管表），所以由托管装配这一层显式补建或校验。
		if cfg.AutoMigrate {
			err = database.MigrateHostedSharedSchema(db)
		} else {
			err = database.RequireHostedSharedSchema(db)
		}
		if err == nil {
			// 精选灵感广场要开箱就有内容，否则新装的实例首页是空的，运营也看不到
			// 该长什么形状。只在表为空时播种，运营接手后不再覆盖。
			err = database.SeedCreationInspirations(db)
		}
	}
	if err != nil {
		cleanupDB()
		return nil, err
	}

	// 托管实例（配置了 HostedFactory）代表平台持有系统渠道的执行凭证，必须用
	// hosted 模式构造服务：本地模式会拒绝系统渠道调用，并把功能开放读成硬编码的
	// 本地默认值。桌面/纯本地装载没有 HostedFactory，保持本地模式不变。
	var svc *app.Service
	if cfg.HostedFactory != nil {
		svc = app.New(repository.New(db), cfg.DataDir)
	} else {
		svc = app.NewLocal(repository.New(db), cfg.DataDir)
	}
	cleanupService := func() {
		_ = svc.Close()
		cleanupDB()
	}
	if err := initializeService(svc); err != nil {
		cleanupService()
		return nil, err
	}
	var hosted HostedExtension
	if cfg.HostedFactory != nil {
		hosted, err = cfg.HostedFactory(HostedDeps{DataDir: cfg.DataDir, Service: svc})
		if err != nil {
			cleanupService()
			return nil, err
		}
	}
	cleanupAll := func() {
		if hosted != nil {
			_ = hosted.Close()
		}
		cleanupService()
	}
	if hosted != nil {
		seeded, seedErr := svc.EnsureHostedFeatureDefaults()
		if seedErr != nil {
			cleanupAll()
			return nil, seedErr
		}
		if seeded {
			log.Printf("hosted: 已写入托管功能开放默认值（自建渠道默认关闭，可在后台开启）")
		}
	}
	providerConfig, configErr := workspace.NewProviderConfig(cfg.DataDir)
	if configErr != nil {
		cleanupAll()
		return nil, configErr
	}
	beefAPIConnection, beefAPIErr := beefapi.New(beefapi.Options{
		DataDir: cfg.DataDir, Provider: providerConfig, ClientVersion: buildinfo.Current().Version,
		FetchCatalog: func(apiKey, baseURL string) ([]beefapi.CatalogModel, error) {
			owner, ownerErr := svc.LocalWorkspaceOwner()
			if ownerErr != nil {
				return nil, ownerErr
			}
			items, catalogErr := svc.FetchChannelModelCatalog(context.Background(), owner, app.ChannelModelsRequest{
				BaseURL: baseURL, APIKey: apiKey, APIFormat: "openai", ChannelID: beefapi.ChannelID, CredentialRef: beefapi.CredentialRef,
			})
			if catalogErr != nil {
				return nil, catalogErr
			}
			models := make([]beefapi.CatalogModel, 0, len(items))
			for _, item := range items {
				models = append(models, beefapi.CatalogModel{
					ID: item.ID, DisplayName: item.DisplayName, ModelType: item.ModelType, SupportedEndpointTypes: item.SupportedEndpointTypes,
				})
			}
			return models, nil
		},
	})
	if beefAPIErr != nil {
		cleanupAll()
		return nil, beefAPIErr
	}
	svc.SetBeefAPI(beefAPIConnection)
	localKernel := app.NewLocalKernel(svc)
	assetService := localasset.New(localKernel, cfg.DataDir)
	projectService := localproject.New(localKernel)
	taskService := localtask.New(localKernel)
	localRoot, err := localapp.New(localapp.Options{
		Workspace: localKernel, Projects: projectService, Assets: assetService, Tasks: taskService,
		Generation: taskService, ProviderConfig: providerConfig, Agent: localKernel, Lifecycle: taskService,
	})
	if err != nil {
		cleanupAll()
		return nil, err
	}

	// 启用托管登录后，工作区由会话决定；桌面版仍固定在本地拥有者上。
	var scope workspace.Context
	if hosted == nil {
		owner, ownerErr := svc.LocalWorkspaceOwner()
		if ownerErr != nil {
			cleanupAll()
			return nil, ownerErr
		}
		scope = workspace.Context{ID: owner.ID, DataDir: cfg.DataDir}
	}

	router := gin.New()
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s\" %d %s %s\n", param.ClientIP, param.TimeStamp.Format(time.RFC3339), param.Method, param.Path, param.StatusCode, param.Latency, param.ErrorMessage)
	}), gin.Recovery())
	router.Use(canvasHandler.RequestCorrelationMiddleware())
	for _, middleware := range cfg.RouterMiddleware {
		router.Use(middleware)
	}
	if cfg.Profile == ProfileDesktop {
		router.Use(desktopCORSMiddleware())
	}
	if hosted != nil {
		router.Use(hosted.WorkspaceMiddleware())
	} else {
		router.Use(canvasHandler.WorkspaceMiddleware(scope))
	}
	api := router.Group("/api")
	status := newSystemStatus(db, svc, true)
	registerSystemStatusRoutes(api, status)
	if hosted != nil {
		hosted.RegisterRoutes(api)
		// 系统渠道目录是托管专属读路径：桌面/本地产物不得暴露它
		// （见 internal/handler/api_test.go 的桌面路由边界断言）。
		canvasHandler.RegisterModelCatalogRoutes(api, svc)
	}
	canvasHandler.RegisterDesktopCanvasAPIWithDependencies(api, svc, canvasHandler.RuntimeDependencies{
		RequestCoordinator: localKernel,
		ProviderConfig:     localRoot.ProviderConfig,
		Assets:             localRoot.Assets,
		Projects:           localRoot.Projects,
		Tasks:              localRoot.Tasks,
		Generation:         localRoot.Generation,
		BeefAPI:            beefAPIConnection,
	})
	if hosted != nil {
		// 平台转发端点是托管专属写路径，且依赖上面注册的 RuntimeDependenciesMiddleware
		// （频控与并发协调从 context 取依赖），因此必须挂在 desktop 路由之后。
		canvasHandler.RegisterSystemRelayRoutes(api, svc)
		// 管理端接口只在托管实例暴露：桌面产物的路由边界断言把它们列为托管专属
		// （见 internal/handler/api_test.go），身份由 /admin 分组的管理员守卫收敛。
		canvasHandler.RegisterAdminRoutes(api, svc)
	}
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "msg": "请求不存在"})
	})

	rootHandler := http.Handler(router)
	launchToken := ""
	if cfg.Profile == ProfileDesktop {
		launchToken = strings.TrimSpace(cfg.LaunchToken)
		if launchToken == "" {
			launchToken, err = httptransport.NewLaunchToken()
			if err != nil {
				cleanupAll()
				return nil, err
			}
		}
		rootHandler = httptransport.RequireLaunchToken(launchToken)(rootHandler)
	}
	return &Runtime{
		cfg:         cfg,
		db:          db,
		service:     svc,
		localApp:    localRoot,
		handler:     rootHandler,
		status:      status,
		launchToken: launchToken,
		beefAPI:     beefAPIConnection,
		hosted:      hosted,
		serveErr:    make(chan error, 1),
	}, nil
}

func initializeService(svc *app.Service) error {
	if err := svc.ValidateRuntime(); err != nil {
		return err
	}
	initializers := []func() error{
		svc.EnsureDefaultPromptTemplates,
		svc.EnsureBuiltinProjectWorkflowTemplate,
		svc.EnsureBuiltinSkills,
		svc.EnsureSkillPackages,
	}
	for _, initialize := range initializers {
		if err := initialize(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) Handler() http.Handler { return r.handler }

func (r *Runtime) Start() error {
	if r == nil || r.closed.Load() {
		return errors.New("运行时已关闭")
	}
	if !r.started.CompareAndSwap(false, true) {
		return nil
	}
	listener, err := net.Listen("tcp", r.cfg.ListenAddr)
	if err != nil {
		r.started.Store(false)
		return err
	}
	r.listener = listener
	r.httpServer = &http.Server{Handler: r.handler, ReadHeaderTimeout: 10 * time.Second}
	if r.localApp != nil {
		r.localApp.Start()
	} else {
		r.service.StartWorker()
	}
	r.background.Add(1)
	go func() {
		defer r.background.Done()
		r.service.BackfillPlaybackTranscodes()
	}()
	r.status.markStarted()
	if r.beefAPI != nil {
		_ = r.beefAPI.Recover(context.Background())
	}
	go func() {
		err := r.httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.serveErr <- fmt.Errorf("HTTP 服务异常退出：%w", err)
		}
		close(r.serveErr)
	}()
	return nil
}

func (r *Runtime) Ready() bool {
	return r != nil && r.started.Load() && !r.closed.Load() && r.status.snapshot(context.Background()).Ready
}

func (r *Runtime) BaseURL() string {
	if r == nil || r.listener == nil {
		return ""
	}
	return "http://" + r.listener.Addr().String() + "/api"
}

func (r *Runtime) LaunchToken() string {
	if r == nil {
		return ""
	}
	return r.launchToken
}

func (r *Runtime) Errors() <-chan error {
	if r == nil {
		closed := make(chan error)
		close(closed)
		return closed
	}
	return r.serveErr
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		r.status.beginDrain()
		if r.beefAPI != nil {
			r.beefAPI.Close()
		}
		var failures []error
		if r.httpServer != nil {
			httpCtx, cancel := context.WithTimeout(ctx, min(30*time.Second, r.cfg.ShutdownTimeout))
			if err := r.httpServer.Shutdown(httpCtx); err != nil {
				_ = r.httpServer.Close()
				failures = append(failures, fmt.Errorf("关闭 HTTP 服务：%w", err))
			}
			cancel()
		}
		workerCtx, cancel := context.WithTimeout(ctx, r.cfg.ShutdownTimeout)
		var workerErr error
		if r.localApp != nil {
			workerErr = r.localApp.Stop(workerCtx)
		} else {
			workerErr = r.service.StopWorker(workerCtx)
		}
		if workerErr != nil {
			failures = append(failures, fmt.Errorf("等待后台任务退出：%w", workerErr))
		}
		cancel()
		backgroundDone := make(chan struct{})
		go func() {
			r.background.Wait()
			close(backgroundDone)
		}()
		select {
		case <-backgroundDone:
		case <-ctx.Done():
			failures = append(failures, fmt.Errorf("等待初始化任务退出：%w", ctx.Err()))
		}
		var serviceErr error
		if r.localApp != nil {
			serviceErr = r.localApp.Close()
		} else {
			serviceErr = r.service.Close()
		}
		if serviceErr != nil {
			failures = append(failures, serviceErr)
		}
		if sqlDB, err := r.db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				failures = append(failures, err)
			}
		}
		if r.hosted != nil {
			if hostedErr := r.hosted.Close(); hostedErr != nil {
				failures = append(failures, hostedErr)
			}
		}
		r.closeErr = errors.Join(failures...)
	})
	return r.closeErr
}

func desktopCORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		}
		c.Header("Access-Control-Allow-Headers", "Accept, Content-Type, X-Desktop-Token, X-Canvas-Trace-ID, X-Idempotency-Key, X-Canvas-Scene, X-Canvas-Upstream-URL, X-Canvas-Upstream-Format, X-Canvas-Upstream-Base-URL")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-Canvas-Trace-ID, X-Diagnostic-Bundle-ID, X-Diagnostic-Schema-Version")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
