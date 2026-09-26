package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/app/resource"
	"github.com/voocel/ainovel-cli/internal/domain/derive"
	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/capability/prompt"
)

type CompileCommand struct {
	ProjectID           string
	Revision            model.Revision
	Kind                model.OperationKind
	WorkerProfileID     string
	Input               json.RawMessage
	Packs               []resource.PackRef
	CreatorProfiles     []resource.CreatorProfileRef
	CoreProtocolVersion string
	CreatedAt           time.Time
}

func (s *Compiler) Compile(ctx context.Context, command CompileCommand) (prompt.Compiled, error) {
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: command.ProjectID}
	revision := command.Revision
	viewKey, err := derive.ViewKey(command.Kind, command.Input)
	if err != nil {
		return prompt.Compiled{}, err
	}
	var intent model.Intent
	var view derive.ModelView
	cached, err := s.store.GetDerivedDocument(ctx, command.ProjectID, revision, derive.ViewKind, viewKey)
	switch {
	case err == nil:
		intentDocument, err := s.store.GetDocument(ctx, target, model.DocumentRef{Kind: model.DocumentIntent, ID: model.SingletonDocumentID}, revision)
		if err != nil {
			return prompt.Compiled{}, err
		}
		if err := json.Unmarshal(intentDocument.Content, &intent); err != nil {
			return prompt.Compiled{}, fmt.Errorf("decode project intent: %w", err)
		}
		if err := json.Unmarshal(cached.Content, &view); err != nil {
			return prompt.Compiled{}, fmt.Errorf("decode cached model view: %w", err)
		}
	case errors.Is(err, model.ErrNotFound):
		project, err := s.projects.Project(ctx, command.ProjectID, revision)
		if err != nil {
			return prompt.Compiled{}, err
		}
		intent = project.Intent
		view, err = derive.BuildModelView(derive.ProjectContent{
			ID: project.ID, Revision: project.Revision, Compass: project.Compass, Plan: project.Plan, Entities: project.Entities, Canon: project.Canon,
			Manuscript: project.Manuscript, Ownership: project.Ownership,
		}, command.Kind, command.Input)
		if err != nil {
			return prompt.Compiled{}, err
		}
		content, err := json.Marshal(view)
		if err != nil {
			return prompt.Compiled{}, fmt.Errorf("encode model view: %w", err)
		}
		// 以落库的那份为准：并发编译同一任务时，所有人拿到同一份视图。
		stored, err := s.store.SaveDerivedDocument(ctx, model.DerivedDocument{
			ProjectID: command.ProjectID, Revision: revision, Kind: derive.ViewKind,
			Key: viewKey, Content: content, CreatedAt: command.CreatedAt,
		})
		if err != nil {
			return prompt.Compiled{}, err
		}
		if err := json.Unmarshal(stored.Content, &view); err != nil {
			return prompt.Compiled{}, fmt.Errorf("decode model view: %w", err)
		}
	default:
		return prompt.Compiled{}, err
	}
	capability, err := prompt.BuiltinCapability(command.Kind)
	if err != nil {
		return prompt.Compiled{}, err
	}
	workerProfileID := command.WorkerProfileID
	if workerProfileID == "" {
		workerProfileID = capability.Worker.ID
	}
	if workerProfileID != capability.Worker.ID {
		return prompt.Compiled{}, fmt.Errorf(
			"operation %s requires worker profile %s, got %s: %w",
			command.Kind, capability.Worker.ID, workerProfileID, model.ErrInvalid,
		)
	}
	worker := capability.Worker
	// 资产自动装配（D31）：命令未显式指定时，按 Project 的固定引用加载已启用的
	// Pack 与 Creator Profile——给这本书固定一套风格与方法，此后自动沿用。
	var overlayRules []string
	if revision > model.InitialRevision {
		assets, err := projectdoc.LoadDocuments[model.ProjectAssetRefs](ctx, s.store, target, model.DocumentAssets, revision)
		if err != nil {
			return prompt.Compiled{}, err
		}
		if len(assets) > 0 {
			if len(command.Packs) == 0 {
				for _, ref := range assets[0].Packs {
					command.Packs = append(command.Packs, resource.PackRef{ID: ref.ID, Revision: ref.Revision})
				}
			}
			if len(command.CreatorProfiles) == 0 {
				for _, ref := range assets[0].CreatorProfiles {
					command.CreatorProfiles = append(command.CreatorProfiles,
						resource.CreatorProfileRef{ID: ref.ID, Scope: ref.Scope, Revision: ref.Revision})
				}
			}
		}
		overlays, err := projectdoc.LoadDocuments[model.ProjectOverlay](ctx, s.store, target, model.DocumentOverlay, revision)
		if err != nil {
			return prompt.Compiled{}, err
		}
		if len(overlays) > 0 {
			overlayRules = overlays[0].Rules
		}
	}
	packs := make([]prompt.VersionedPack, len(command.Packs))
	for i, ref := range command.Packs {
		pack, err := resource.LoadPack(ctx, s.store, ref)
		if err != nil {
			return prompt.Compiled{}, err
		}
		packs[i] = pack
	}
	creatorProfiles := make([]prompt.VersionedCreatorProfile, len(command.CreatorProfiles))
	for i, ref := range command.CreatorProfiles {
		profile, err := resource.LoadCreatorProfile(ctx, s.store, ref)
		if err != nil {
			return prompt.Compiled{}, err
		}
		creatorProfiles[i] = profile
	}
	ownership, err := json.Marshal(view.Ownership)
	if err != nil {
		return prompt.Compiled{}, fmt.Errorf("encode ownership view: %w", err)
	}
	storyContext, err := json.Marshal(view.Context)
	if err != nil {
		return prompt.Compiled{}, fmt.Errorf("encode story context: %w", err)
	}
	return s.prompts.Reload(ctx, prompt.CompileRequest{
		ProjectID: command.ProjectID, CoreProtocolVersion: command.CoreProtocolVersion,
		Worker: worker, Packs: packs, CreatorProfiles: creatorProfiles,
		Intent: intent, Ownership: ownership, OverlayRules: overlayRules,
		StoryContext: storyContext, Task: view.Task,
		BaseRevision: revision, ProjectOverlayRevision: revision,
	}, command.CreatedAt)
}
