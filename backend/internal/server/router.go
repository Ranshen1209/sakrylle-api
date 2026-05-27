package server

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/web"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	frameSrcRefreshTimeout    = 5 * time.Second
	oauthOriginRefreshTimeout = 5 * time.Second
	// oauthOriginRefreshInterval bounds how long after registering a new
	// OAuth client an admin must wait before its redirect_uri origin starts
	// being echoed in CORS responses. 5 min matches operator expectations
	// for "low-frequency config changes" elsewhere in the stack and avoids
	// hammering the DB.
	oauthOriginRefreshInterval = 5 * time.Minute
)

// SetupRouter 配置路由器中间件和路由
func SetupRouter(
	r *gin.Engine,
	handlers *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	oauthProviderService *service.OAuthProviderService,
	cfg *config.Config,
	redisClient *redis.Client,
) *gin.Engine {
	// 缓存 iframe 页面的 origin 列表，用于动态注入 CSP frame-src
	var cachedFrameOrigins atomic.Pointer[[]string]
	emptyOrigins := []string{}
	cachedFrameOrigins.Store(&emptyOrigins)

	refreshFrameOrigins := func() {
		ctx, cancel := context.WithTimeout(context.Background(), frameSrcRefreshTimeout)
		defer cancel()
		origins, err := settingService.GetFrameSrcOrigins(ctx)
		if err != nil {
			// 获取失败时保留已有缓存，避免 frame-src 被意外清空
			return
		}
		cachedFrameOrigins.Store(&origins)
	}
	refreshFrameOrigins() // 启动时初始化

	// 缓存 OAuth client 的 redirect_uri origin 列表，用于 /oauth/token 等
	// 端点的跨域响应。Public PKCE 客户端（浏览器 SPA）必须直连
	// /oauth/token 兑换 authorization code，因此其 origin 必须出现在
	// Access-Control-Allow-Origin 响应头中。
	//
	// nil-safe：oauthProviderService 在 setup 流程中可能为 nil，此时禁用动态
	// allowlist；CORS 中间件继续仅依据 cfg.CORS.AllowedOrigins 工作。
	var cachedOAuthOrigins atomic.Pointer[[]string]
	cachedOAuthOrigins.Store(&emptyOrigins)

	if oauthProviderService != nil {
		refreshOAuthOrigins := func() {
			ctx, cancel := context.WithTimeout(context.Background(), oauthOriginRefreshTimeout)
			defer cancel()
			origins, err := oauthProviderService.AllowedClientOrigins(ctx)
			if err != nil {
				log.Printf("CORS: failed to refresh OAuth client origins, keeping previous snapshot: %v", err)
				return
			}
			cachedOAuthOrigins.Store(&origins)
		}
		refreshOAuthOrigins() // 启动时初始化

		// 5 分钟周期性刷新。新增/禁用 OAuth client 后，新 origin 在该窗口内生效。
		// 不取消 ticker —— 路由器与进程同生共死。
		go func() {
			ticker := time.NewTicker(oauthOriginRefreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				refreshOAuthOrigins()
			}
		}()
	}

	// 应用中间件
	r.Use(middleware2.RequestLogger())
	r.Use(middleware2.Logger())
	r.Use(middleware2.CORS(cfg.CORS, func() []string {
		if p := cachedOAuthOrigins.Load(); p != nil {
			return *p
		}
		return nil
	}))
	r.Use(middleware2.SecurityHeaders(cfg.Security.CSP, func() []string {
		if p := cachedFrameOrigins.Load(); p != nil {
			return *p
		}
		return nil
	}))

	// Serve embedded frontend with settings injection if available
	if web.HasEmbeddedFrontend() {
		frontendServer, err := web.NewFrontendServer(settingService)
		if err != nil {
			log.Printf("Warning: Failed to create frontend server with settings injection: %v, using legacy mode", err)
			r.Use(web.ServeEmbeddedFrontend())
			settingService.SetOnUpdateCallback(refreshFrameOrigins)
		} else {
			// Register combined callback: invalidate HTML cache + refresh frame origins
			settingService.SetOnUpdateCallback(func() {
				frontendServer.InvalidateCache()
				refreshFrameOrigins()
			})
			r.Use(frontendServer.Middleware())
		}
	} else {
		settingService.SetOnUpdateCallback(refreshFrameOrigins)
	}

	// 注册路由
	registerRoutes(r, handlers, jwtAuth, adminAuth, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, oauthProviderService, cfg, redisClient)

	return r
}

// registerRoutes 注册所有 HTTP 路由
func registerRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	oauthProviderService *service.OAuthProviderService,
	cfg *config.Config,
	redisClient *redis.Client,
) {
	// 通用路由（健康检查、状态等）
	routes.RegisterCommonRoutes(r)

	// API v1
	v1 := r.Group("/api/v1")

	// 注册各模块路由
	routes.RegisterAuthRoutes(v1, h, jwtAuth, redisClient, settingService)
	routes.RegisterUserRoutes(v1, h, jwtAuth, settingService)
	routes.RegisterAdminRoutes(v1, h, adminAuth, settingService)
	routes.RegisterGatewayRoutes(r, h, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, oauthProviderService, cfg)
	routes.RegisterPaymentRoutes(v1, h.Payment, h.PaymentWebhook, h.Admin.Payment, jwtAuth, adminAuth, settingService)
	routes.RegisterOAuthRoutes(r, v1, h, jwtAuth, redisClient)
	routes.RegisterOAuthDeviceRoutes(r, v1, h, jwtAuth, redisClient)

	handler.RegisterPageRoutes(v1, cfg.Pricing.DataDir, gin.HandlerFunc(jwtAuth), gin.HandlerFunc(adminAuth), settingService)
}
