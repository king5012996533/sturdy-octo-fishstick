package app

import (
	"context"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/asset"
)

// fakeReferenceImageServer 提供一张可解码的参考图，并记下被请求了几次。
func fakeReferenceImageServer(t *testing.T, width, height int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, testGradientImage(width, height))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// 收下来的参考图必须被重编码：上游单张 3MB 的原图照存，全池就是几百兆，
// 而"使用这个创意"时用户还要把它下载一遍再传回自己的资源库。
func TestHarvestInspirationReferenceImageDownsizes(t *testing.T) {
	server, _ := fakeReferenceImageServer(t, 2400, 1350)

	svc := newInspirationTestService(t)
	store := asset.NewFileStore(t.TempDir())
	resourceID, err := svc.harvestInspirationReferenceImage(context.Background(), server.Client(), store, server.URL+"/ref/big.png", false)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := svc.repo.Resource(resourceID)
	if err != nil || resource == nil {
		t.Fatalf("参考图应落库：%v", err)
	}
	if resource.MimeType != "image/jpeg" || resource.UserID != InspirationReferenceOwnerID {
		t.Fatalf("应重编码成 JPEG 并归平台虚拟用户，实际 %+v", resource)
	}
	if resource.Size <= 0 || resource.Size > 400<<10 {
		t.Fatalf("2400px 的图重编码后应远小于 400KB，实际 %d 字节", resource.Size)
	}
}

// 重跑一次抓取必须复用已有产物，而不是再下载一次、再建一行资源。
func TestHarvestInspirationReferenceImageReusesExisting(t *testing.T) {
	server, hits := fakeReferenceImageServer(t, 800, 600)
	source := server.URL + "/ref/small.png"

	svc := newInspirationTestService(t)
	store := asset.NewFileStore(t.TempDir())
	first, err := svc.harvestInspirationReferenceImage(context.Background(), server.Client(), store, source, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.harvestInspirationReferenceImage(context.Background(), server.Client(), store, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("资源 ID 应由源地址派生，实际 %q vs %q", first, second)
	}
	if hits.Load() != 1 {
		t.Fatalf("第二次应复用已有产物、不再下载，实际请求了 %d 次", hits.Load())
	}
}

// 上游图床的地址要走"先缩后传"：上游一张角色参考图是 5504×3072 的 19MB PNG，而我们
// 最终只留 1280px。让上游先缩到 2048 再传，一次全量抓取的图片流量降一个数量级。
// 别的 host 一律不动——签过名的地址多一个查询串就是 403。
func TestInspirationReferenceFetchURLResizesUpstreamHost(t *testing.T) {
	const source = "https://libtv-res.liblib.art/upload-images/aa/bb.png"
	got := inspirationReferenceFetchURL(source)
	if !strings.HasPrefix(got, source+"?") || !strings.Contains(got, "m_lfit,w_2048,h_2048") {
		t.Fatalf("上游图床应挂上缩图参数，实际 %q", got)
	}
	for _, untouched := range []string{
		"https://liblibai-online.liblib.cloud/upload-images/aa/bb.png",
		"https://cdn.example.com/ref/1.png",
		// 自带处理参数的地址不叠第二个 x-oss-process：上游取哪一个都说不准。
		"https://libtv-res.liblib.art/upload-images/aa/bb.png?x-oss-process=image/info",
		"not a url",
	} {
		if got := inspirationReferenceFetchURL(untouched); got != untouched {
			t.Fatalf("不该改写的地址被改写了：%q -> %q", untouched, got)
		}
	}
	// 资源 ID 仍然由原始地址派生：换个缩图参数不该让同一张图变成另一行资源。
	if inspirationReferenceResourceID(source) == inspirationReferenceResourceID(got) {
		t.Fatal("资源 ID 不该跟着请求参数变")
	}
}

// 解不出来的字节（防盗链提示页、webp 之类）不收：往资源库里塞一个前台显示不了、
// 生成侧也读不懂的文件，比少一张参考图更糟。
func TestHarvestInspirationReferenceImageRejectsUndecodable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "<html>hotlink denied</html>")
	}))
	defer server.Close()

	svc := newInspirationTestService(t)
	store := asset.NewFileStore(t.TempDir())
	if _, err := svc.harvestInspirationReferenceImage(context.Background(), server.Client(), store, server.URL+"/denied.png", false); err == nil {
		t.Fatal("不可解码的响应应报错")
	}
}

// 多条参考图按源地址顺序落库：顺序就是原作喂给模型的顺序，乱序等于把主体图当成场景图。
func TestHarvestInspirationReferenceImagesKeepsOrder(t *testing.T) {
	server, _ := fakeReferenceImageServer(t, 400, 300)
	sources := []string{server.URL + "/ok-1.png", server.URL + "/ok-2.png", server.URL + "/ok-3.png"}

	svc := newInspirationTestService(t)
	store := asset.NewFileStore(t.TempDir())
	ids, failed, _ := svc.harvestInspirationReferenceImages(context.Background(), server.Client(), store, sources, false)
	if len(ids) != len(sources) || failed != 0 {
		t.Fatalf("三张都应收下，实际 ids=%v failed=%d", ids, failed)
	}
	for index, source := range sources {
		if ids[index] != inspirationReferenceResourceID(source) {
			t.Fatalf("第 %d 张的顺序不对：预期 %q，实际 %q", index, inspirationReferenceResourceID(source), ids[index])
		}
	}
}

// 上游返回非 200 时报错并带上状态码，便于在命令输出里看出是防盗链还是 404。
func TestDownloadInspirationReferenceImageReportsStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	_, err := downloadInspirationReferenceImage(context.Background(), server.Client(), server.URL+"/x.png")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("应报出上游状态码，实际 %v", err)
	}
}

// 配置了公网地址时，视图必须给出带签名的参考图地址（而不是上游外链）；
// 没配时整块退化成空数组，但提示词与参数照常可用——那是一份"不完整但能用"的配方，
// 比整条作品的复用入口失效好。
func TestCreationInspirationViewSignsRecipeImages(t *testing.T) {
	const uuid = "385c4036c86441a29994f53d57011e64"
	fake := newFakeLibtvTemplateDetail(t, uuid, true, 2)
	withTemplateDetailBaseURL(t, fake.server.URL)
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")

	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_RECIPE_1", "https://www.liblib.tv/detail/"+uuid)
	if _, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{}); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := svc.creationInspirationView(saved)
	if len(view.RecipeImageURLs) != 2 {
		t.Fatalf("应给出 2 个签名地址，实际 %v", view.RecipeImageURLs)
	}
	for _, url := range view.RecipeImageURLs {
		if !strings.HasPrefix(url, "https://example.com/api/public/resources/") {
			t.Fatalf("参考图应走平台签名出口，实际 %q", url)
		}
	}
	if view.RecipeVideoModel != "star-video2" || view.RecipeDurationSeconds != 15 {
		t.Fatalf("配方应随视图返回，实际 %+v", view)
	}
}
