package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"
	packloader "github.com/voocel/ainovel-cli/internal/infra/capability/pack"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

// OfficialPackID 是官方内置包的保留 ID（D65），用户安装的包不得占用。
const OfficialPackID = "official"

// Official 让随二进制发布的官方包以普通 Pack 的身份进入权威存储（D65）：
// 清单、校验、版本与启用全部沿用用户 Pack 的机制，只有来源是编译期嵌入。
type Official struct {
	store   *store.Store
	changes *change.Engine
	source  fs.FS
}

func NewOfficial(authorityStore *store.Store, changes *change.Engine, source fs.FS) *Official {
	return &Official{store: authorityStore, changes: changes, source: source}
}

// Ensure 返回内容与内置官方包一致的版本：库里当前版本相同就复用，否则以触发它的
// 用户提交新版本。已固定旧版本的作品不受影响（D31）。
func (o *Official) Ensure(ctx context.Context, userID string, at time.Time) (model.ProjectPackRef, error) {
	loaded, err := packloader.LoadFS(o.source)
	if err != nil {
		return model.ProjectPackRef{}, fmt.Errorf("load official pack: %w", err)
	}
	if loaded.Manifest.ID != OfficialPackID {
		return model.ProjectPackRef{}, fmt.Errorf("official pack declares id %q: %w", loaded.Manifest.ID, model.ErrInvalid)
	}
	content, err := json.Marshal(loaded.Manifest)
	if err != nil {
		return model.ProjectPackRef{}, fmt.Errorf("encode official pack: %w", err)
	}
	target := model.AuthorityTarget{Kind: model.AuthorityPack, ID: OfficialPackID}
	base, err := currentRevision(ctx, o.store, target)
	if err != nil {
		return model.ProjectPackRef{}, err
	}
	if base > model.InitialRevision {
		current, err := o.store.GetDocument(ctx, target, model.DocumentRef{Kind: model.DocumentPack, ID: OfficialPackID}, base)
		if err != nil {
			return model.ProjectPackRef{}, err
		}
		if bytes.Equal(current.Content, content) {
			return model.ProjectPackRef{ID: OfficialPackID, Revision: base}, nil
		}
	}
	// 提案 ID 绑定基线与内容：同一次同步重入幂等，换回旧内容也会生成新版本。
	changeID := fmt.Sprintf("pack:%s:r%d:%s", OfficialPackID, base, loaded.Digest)
	committed, err := commitPack(ctx, o.changes, target, base, content, changeID, userID, "同步官方内置包 v"+loaded.Manifest.Version, at)
	if err != nil {
		return model.ProjectPackRef{}, err
	}
	return model.ProjectPackRef{ID: OfficialPackID, Revision: committed.NewRevision}, nil
}
