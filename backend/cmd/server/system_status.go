package main

import (
	"context"
	"net/http"
	"sync/atomic"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/buildinfo"
	"infinite-canvas/backend/internal/database"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type systemStatus struct {
	db       *gorm.DB
	service  *app.Service
	started  atomic.Bool
	draining atomic.Bool
}

type systemStatusSnapshot struct {
	Status            string                `json:"status"`
	Ready             bool                  `json:"ready"`
	Started           bool                  `json:"started"`
	Draining          bool                  `json:"draining"`
	ActiveWorkerTasks int64                 `json:"activeWorkerTasks"`
	Build             buildinfo.Info        `json:"build"`
	Schema            database.SchemaStatus `json:"schema"`
	Checks            systemStatusChecks    `json:"checks"`
}

type systemStatusChecks struct {
	Database bool `json:"database"`
	Runtime  bool `json:"runtime"`
	Schema   bool `json:"schema"`
}

// systemStatusPublicSnapshot 是匿名探活唯一可见的字段集合。
//
// 刻意不含 build（版本 / 提交 / 构建时间 / Go 版本）与 schema（内部表结构版本）：
// 探活只需要回答"这台机器现在能不能服务"。匿名端点把它们送出去等于白给一份版本指纹——
// 攻击者据此就能直接对上已知漏洞；schema 版本还会暴露内部表结构的推进进度。
// 需要这些信息的运维链路走 /system/version，那条在托管形态下要求登录。
type systemStatusPublicSnapshot struct {
	Status            string             `json:"status"`
	Ready             bool               `json:"ready"`
	Started           bool               `json:"started"`
	Draining          bool               `json:"draining"`
	ActiveWorkerTasks int64              `json:"activeWorkerTasks"`
	Checks            systemStatusChecks `json:"checks"`
}

func (s systemStatusSnapshot) public() systemStatusPublicSnapshot {
	return systemStatusPublicSnapshot{
		Status:            s.Status,
		Ready:             s.Ready,
		Started:           s.Started,
		Draining:          s.Draining,
		ActiveWorkerTasks: s.ActiveWorkerTasks,
		Checks:            s.Checks,
	}
}

func newSystemStatus(db *gorm.DB, svc *app.Service) *systemStatus {
	return &systemStatus{db: db, service: svc}
}

func (s *systemStatus) markStarted() { s.started.Store(true) }

func (s *systemStatus) beginDrain() {
	s.draining.Store(true)
	if s.service != nil {
		s.service.BeginDrain()
	}
}

func (s *systemStatus) snapshot(ctx context.Context) systemStatusSnapshot {
	snapshot := systemStatusSnapshot{
		Started:  s.started.Load(),
		Draining: s.draining.Load(),
		Build:    buildinfo.Current(),
		Schema:   database.SchemaStatus{Expected: database.CurrentSchemaVersion},
	}
	if s.service != nil {
		snapshot.ActiveWorkerTasks = s.service.ActiveWorkerTasks()
		snapshot.Checks.Runtime = s.service.ValidateRuntime() == nil
	}
	if s.db != nil {
		snapshot.Checks.Database = s.db.WithContext(ctx).Exec("SELECT 1").Error == nil
		if schema, err := database.ReadSchemaStatus(s.db); err == nil {
			snapshot.Schema = schema
			snapshot.Checks.Schema = schema.Ready
		}
	}
	snapshot.Ready = snapshot.Started && !snapshot.Draining && snapshot.Checks.Database && snapshot.Checks.Runtime && snapshot.Checks.Schema
	switch {
	case snapshot.Draining:
		snapshot.Status = "draining"
	case snapshot.Ready:
		snapshot.Status = "ok"
	case !snapshot.Started:
		snapshot.Status = "starting"
	default:
		snapshot.Status = "unhealthy"
	}
	return snapshot
}

func registerSystemStatusRoutes(api *gin.RouterGroup, status *systemStatus) {
	api.GET("/health/live", func(c *gin.Context) {
		// 存活探针只回答"进程还在不在"，连 build 都不给。
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"status": "ok"}, "msg": "ok"})
	})
	startup := func(c *gin.Context) {
		snapshot := status.snapshot(c.Request.Context())
		if !snapshot.Started {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "data": snapshot.public(), "msg": "服务仍在启动"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": snapshot.public(), "msg": "ok"})
	}
	ready := func(c *gin.Context) {
		snapshot := status.snapshot(c.Request.Context())
		if !snapshot.Ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "data": snapshot.public(), "msg": "服务暂未就绪"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": snapshot.public(), "msg": "ok"})
	}
	api.GET("/health/startup", startup)
	api.GET("/health/ready", ready)
	api.GET("/health", ready)
	api.GET("/system/version", func(c *gin.Context) {
		snapshot := status.snapshot(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"build": snapshot.Build, "schema": snapshot.Schema}, "msg": "ok"})
	})
}
