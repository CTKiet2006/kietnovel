package resource

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"
	packloader "github.com/voocel/ainovel-cli/internal/infra/capability/pack"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

func officialSource(draft string) fstest.MapFS {
	return fstest.MapFS{
		"pack.jsonc":       {Data: []byte(`{"id":"official","version":"1","name":"官方基线","prompts":{"writer.chapter_draft":"prompts/draft.md"}}`)},
		"prompts/draft.md": {Data: []byte(draft)},
	}
}

func openOfficialStore(t *testing.T) (*store.Store, *change.Engine) {
	t.Helper()
	authorityStore, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "ainovel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { authorityStore.Close() })
	return authorityStore, change.New(authorityStore)
}

// 内容不变就复用当前版本；内置内容变化才提交新版本，旧版本仍可按号读取（D31）。
func TestOfficialEnsureCommitsOnlyWhenContentChanges(t *testing.T) {
	ctx := context.Background()
	authorityStore, changes := openOfficialStore(t)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	first, err := NewOfficial(authorityStore, changes, officialSource("用场景推进")).Ensure(ctx, "user-1", now)
	if err != nil || first != (model.ProjectPackRef{ID: OfficialPackID, Revision: 1}) {
		t.Fatalf("first ensure = %#v, %v", first, err)
	}
	again, err := NewOfficial(authorityStore, changes, officialSource("用场景推进")).Ensure(ctx, "user-2", now.Add(time.Hour))
	if err != nil || again != first {
		t.Fatalf("unchanged ensure = %#v, %v", again, err)
	}
	updated, err := NewOfficial(authorityStore, changes, officialSource("用场景推进，前情不复述")).Ensure(ctx, "user-1", now.Add(2*time.Hour))
	if err != nil || updated.Revision != 2 {
		t.Fatalf("changed ensure = %#v, %v", updated, err)
	}
	old, err := LoadPack(ctx, authorityStore, PackRef{ID: OfficialPackID, Revision: first.Revision})
	if err != nil || old.Manifest.PromptOverlays["writer.chapter_draft"] != "用场景推进" {
		t.Fatalf("pinned old revision = %#v, %v", old, err)
	}
}

// 新书在创建提交中固定官方包；浮动的官方引用先同步再固定；用户包不得占用保留 ID。
func TestOfficialPackIsEnabledForNewBooksAndReserved(t *testing.T) {
	ctx := context.Background()
	authorityStore, changes := openOfficialStore(t)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	official := NewOfficial(authorityStore, changes, officialSource("用场景推进"))
	projects := projectdoc.New(authorityStore, changes, official)
	catalog := New(authorityStore, changes, projects, official, nil)

	project, err := projects.CreateProject(ctx, projectdoc.CreateProjectCommand{
		ProjectID: "book-1", ChangeID: "create", UserID: "user-1", Reason: "创建作品",
		Draft: projectdoc.ProjectDraft{Intent: model.Intent{Premise: "凡人修仙"}}, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.Assets == nil || !slices.Equal(project.Assets.Packs, []model.ProjectPackRef{{ID: OfficialPackID, Revision: 1}}) {
		t.Fatalf("new book assets = %#v", project.Assets)
	}

	official.source = officialSource("用场景推进，前情不复述")
	result, err := catalog.SetProjectAssets(ctx, SetProjectAssetsCommand{
		ProjectID: "book-1", ChangeID: "upgrade", UserID: "user-1",
		Packs: []PackRef{{ID: OfficialPackID}}, Reason: "升级官方包", CreatedAt: now.Add(time.Hour),
	})
	if err != nil || !slices.Equal(result.Assets.Packs, []model.ProjectPackRef{{ID: OfficialPackID, Revision: 2}}) {
		t.Fatalf("floating official ref = %#v, %v", result.Assets, err)
	}

	loaded, err := packloader.LoadFS(officialSource("冒名"))
	if err != nil {
		t.Fatalf("load copy: %v", err)
	}
	if _, err := catalog.installPack(ctx, loaded, "install-copy", "user-1", "安装", now); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("reserved id install error = %v", err)
	}
}
