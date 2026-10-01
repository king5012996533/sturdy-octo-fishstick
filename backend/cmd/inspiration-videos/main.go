// inspiration-videos 给灵感广场的视频条目补上成片地址，供前台按需播放。
//
// 和 inspiration-covers 是两件事，也刻意做成两个命令：封面要抓回本地（几十 KB，
// 抓完就不再依赖上游），成片只记地址（几百 MB，永远留在上游）。把这两条合进一个
// 命令，会让"封面必须自持、成片只能热链"这个区别在代码里消失。
//
// 命令只写灵感表，可以在线跑；过程中会逐条访问作品页（每条几百 KB），建议避开高峰。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	limit := flag.Int("limit", 0, "本次最多处理多少条（0 表示全部）")
	overwrite := flag.Bool("overwrite", false, "重新取一遍已有地址（上游换了成片时用）")
	flag.Parse()

	dataDir := strings.TrimSpace(os.Getenv("CANVAS_BACKEND_DATA_DIR"))
	if dataDir == "" {
		dataDir = "data"
	}
	db, err := database.Open(database.Config{
		Driver:  strings.TrimSpace(os.Getenv("CANVAS_DATABASE_DRIVER")),
		DSN:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DataDir: dataDir,
	})
	if err != nil {
		return err
	}
	if err := database.ConfigurePool(db); err != nil {
		return err
	}
	// 逐条判断是否已有地址时会产生 record not found，那是正常分支不是故障。
	db.Logger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             2 * time.Second,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
	})
	service := app.New(repository.New(db), dataDir)
	defer func() { _ = service.Close() }()

	result, err := service.HarvestInspirationVideos(context.Background(), app.InspirationVideoHarvestOptions{
		Overwrite: *overwrite,
		Limit:     *limit,
	})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if result.Failed > 0 {
		// 有失败就返回非零退出码：作品下架、页面改版这类问题会成片出现，
		// 静默成功会让"广场上有一半卡片点不动"变成一个没人发现的常态。
		return fmt.Errorf("%d 条成片地址抓取失败", result.Failed)
	}
	if result.ImageFailures > 0 {
		// 参考图失败同样返回非零：参考图正是"复刻不出来"的主因，而它失败时成片
		// 仍能播放，条目级的成功会把它盖住。解不出来的格式（webp 之类）会成片出现。
		return fmt.Errorf("%d 张参考图抓取失败", result.ImageFailures)
	}
	return nil
}
