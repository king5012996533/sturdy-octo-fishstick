// inspiration-covers 把灵感广场里仍指向外部图床的封面抓回本地资源库。
//
// 放在命令里而不是做成后台任务：这是"上游换图/发现盗链"时才需要跑一次的一次性维护，
// 做成常驻任务就得考虑并发、重试队列和失败告警，而它一年可能只跑两三次。运营也确实
// 需要一个能当场看明白结果的入口——输出的每条明细都带源地址、产物大小与失败原因。
//
// 抓取只写资源表与灵感表，可以在线跑；但过程中会连续访问上游图床，建议避开前台高峰。
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
	overwrite := flag.Bool("overwrite", false, "重新下载已有封面（上游换了图但地址没变时用）")
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
	// 抓取的第一件事是"这条封面是不是已经抓过了"，那必然产生一批 record not found。
	// 它们是正常分支而不是故障；不压掉的话，日志会被这几十行淹没，
	// 真正需要看一眼的失败原因反而找不到。
	db.Logger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             2 * time.Second,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
	})
	service := app.New(repository.New(db), dataDir)
	defer func() { _ = service.Close() }()

	result, err := service.HarvestInspirationCovers(context.Background(), app.InspirationCoverHarvestOptions{
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
		// 有失败就返回非零退出码：截断的上游响应、防盗链这类问题会成片出现，
		// 让调用方（或一次人工回看）一眼看出这批没抓全。
		return fmt.Errorf("%d 条封面抓取失败", result.Failed)
	}
	return nil
}
