// storage-compact 是离线维护命令：把历史任务结果里内联的生成媒体转存进资源表，
// 让结果只保留引用，然后回收 SQLite 空间。写入与 VACUUM 都需要独占数据库，
// 必须在后端停机时运行。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/repository"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	vacuum := flag.Bool("vacuum", true, "压缩完成后执行 VACUUM 回收磁盘空间")
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
	repo := repository.New(db)
	service := app.New(repo, dataDir)
	defer func() { _ = service.Close() }()

	summary, err := service.CompactInlineTaskMedia()
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	if len(summary.Failed) > 0 {
		return fmt.Errorf("%d 个任务的媒体压缩失败，未回收空间", len(summary.Failed))
	}
	if *vacuum {
		if err := repo.Vacuum(); err != nil {
			return err
		}
		fmt.Println("vacuum 完成")
	}
	return nil
}
