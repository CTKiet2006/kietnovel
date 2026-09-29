package imp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

func mustLoadState(t *testing.T, w *Workspace) Facts {
	t.Helper()
	f, err := LoadState(w)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return f
}

func TestNextActionChain(t *testing.T) {
	cases := []struct {
		name string
		f    Facts
		want Action
	}{
		{"空", Facts{}, ActionIngest},
		{"已建区待切分", Facts{WorkspaceReady: true}, ActionSegment},
		{"已切分待确认", Facts{WorkspaceReady: true, Segmented: true}, ActionAwaitConfirmation},
		{"已确认待分析", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3}, ActionAnalyze},
		{"分析未满", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 2}, ActionAnalyze},
		{"分析齐待综合", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3}, ActionSynthesize},
		{"综合后 uncertain 待裁定", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true}, ActionAwaitStoryResolution},
		{"uncertain 已裁定待发布", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true, StoryResolved: true}, ActionPublish},
		{"明确状态待发布", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true}, ActionPublish},
		{"全部一致", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, Published: true}, ActionDone},
		{"发布终态短路上游失鲜", Facts{Published: true}, ActionDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextAction(c.f)
			if got != c.want {
				t.Fatalf("NextAction=%s want=%s", got, c.want)
			}
			// Constant for the same fact snapshot.
			if NextAction(c.f) != got {
				t.Fatal("NextAction 对同一 Facts 不恒定")
			}
		})
	}
}

func TestLoadStateReflectsWorkspace(t *testing.T) {
	book := t.TempDir()
	// Workspace not yet created: not active -> ingest.
	w := OpenWorkspace(book)
	if NextAction(mustLoadState(t, w)) != ActionIngest {
		t.Fatal("空书应先 ingest")
	}
	// After creation: workspace ready, not segmented -> segment.
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一章\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	f := mustLoadState(t, ws)
	if !f.WorkspaceReady || f.Segmented {
		t.Fatalf("建区后事实不符：%+v", f)
	}
	if NextAction(f) != ActionSegment {
		t.Fatal("建区后应 segment")
	}
}

func TestLoadStateReportsCorruptArtifact(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一章\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.writeAtomic(fileSegmentation, []byte("{")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(ws); err == nil || !strings.Contains(err.Error(), "切分工件") {
		t.Fatalf("损坏工件不得伪装成尚未切分: %v", err)
	}
}

func TestIngestSnapshotConsistent(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	content := "第一章\r\n正文一\r\n\r\n第二章\r\n正文二"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, m, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if m.Encoding != encodingUTF8 || m.SourceName != "book.txt" {
		t.Fatalf("manifest 不符：%+v", m)
	}
	snap, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	// The source snapshot must already be normalized and its digest must agree with the manifest.
	if string(snap) != "第一章\n正文一\n\n第二章\n正文二" {
		t.Fatalf("源快照未归一化：%q", snap)
	}
	if Digest(snap) != m.NormalizedSHA256 {
		t.Fatal("源快照摘要与 manifest 不一致")
	}
}

// TestGuidanceChangeInvalidatesSegmentation guards §18.3: segmentation guidance is a semantic input of segmentation,
// so a guidance change naturally mismatches and redoes the old segmentation (and everything downstream), with no manual invalidation rule needed.
func TestGuidanceChangeInvalidatesSegmentation(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("第一章\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一章", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	if !mustLoadState(t, ws).Segmented {
		t.Fatal("无指导时切分应有效")
	}
	if err := ws.writeAtomic(fileGuidance, []byte("幕间也是独立章节")); err != nil {
		t.Fatal(err)
	}
	if mustLoadState(t, ws).Segmented {
		t.Fatal("指导变化后旧切分应失效（需重识别）")
	}
}

// TestResumeSummary guards the §18.2 startup notice: with no workspace it returns an empty string; when stuck mid-way it gives a stage-specific description,
// so the user does not have to wait until their writing is rejected by the gate to learn that this book is stuck mid-import.
func TestResumeSummary(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("无导入工作区应返回空串，得 %q", got)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("第一章\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "尚未完成切分") {
		t.Fatalf("刚建区应提示未完成切分，得 %q", got)
	}
	// Segmentation + confirmation ready, analysis 0/1 -> report analysis progress.
	norm, _ := ws.LoadSource()
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一章", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	raw, _ := ws.readBytes(fileSegmentation)
	if err := writeArtifact(ws, fileConfirmation, Digest(raw), Confirmation{Method: confirmMethodAuto, Chapters: 1}); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "已分析 0/1 章") {
		t.Fatalf("应提示分析进度，得 %q", got)
	}
}

// TestResumeStatusPublishedIsTerminal guards the published terminal state (from a real incident): once the book is fully published,
// a segmentPromptVersion upgrade makes the workspace segmentation artifact stale, and ResumeStatus must not on that basis judge the book back into
// "mid-import" -- otherwise startEngine's cross-restart gate would permanently refuse to start continued writing on an already published book.
func TestResumeStatusPublishedIsTerminal(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("第一章\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, _ := ws.LoadSource()
	// Write the segmentation with an old version number: simulates the digest mismatch caused by a post-publish prompt upgrade.
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "第一章", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", "seg-v0"), seg); err != nil {
		t.Fatal(err)
	}
	// Unpublished + stale segmentation: still a mid-import, the gate should block.
	if active, done, err := ResumeStatus(st); err != nil || !active || done {
		t.Fatalf("未发布的失鲜工作区应判未完成（active=%v done=%v）", active, done)
	}
	// The official store was written in full from that segmentation -> publish reconciliation passes, the terminal state is unaffected by upstream staleness.
	if err := st.Book.Save(domain.BookMetadata{Title: "测试书", Synopsis: "测试简介"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("前提"); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "第一章"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatal(err)
	}
	if active, done, err := ResumeStatus(st); err != nil || !active || !done {
		t.Fatalf("已发布书应判导入完成（active=%v done=%v）", active, done)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("已发布书不应提示未完成导入，得 %q", got)
	}
}

func TestImportPreconditions(t *testing.T) {
	// An empty book passes.
	empty := store.NewStore(t.TempDir())
	if err := checkImportPreconditions(empty); err != nil {
		t.Fatalf("空书应通过前置校验：%v", err)
	}
	// Having completed chapters is rejected.
	nonEmpty := store.NewStore(t.TempDir())
	if err := nonEmpty.Progress.Save(&domain.Progress{CompletedChapters: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(nonEmpty); err == nil {
		t.Fatal("非空书应被拒绝导入")
	}
	withBook := store.NewStore(t.TempDir())
	if err := withBook.Book.Save(domain.BookMetadata{Title: "已有作品", Synopsis: "已有简介"}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(withBook); err == nil {
		t.Fatal("已有作品信息时应被拒绝导入")
	}
}
