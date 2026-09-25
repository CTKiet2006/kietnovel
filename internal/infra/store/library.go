package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// ProjectSummary reads only the intent, the compass (nil when absent) and the live
// planned and written chapter counts. Manuscript bodies never leave SQLite when
// listing the library.
type ProjectSummary struct {
	ID      string
	Intent  json.RawMessage
	Compass json.RawMessage
	Planned int
	Written int
}

// liveCount 统计作品当前 Revision 上仍存在的某类文档（最新版本为 put），filter 追加内容条件。
func liveCount(filter string) string {
	return `(SELECT COUNT(*) FROM document_versions d
   WHERE d.target_kind=a.target_kind AND d.target_id=a.target_id AND d.target_scope=''
    AND d.document_kind=? AND d.operation='put' AND d.revision<=a.current_revision` + filter + `
    AND NOT EXISTS (SELECT 1 FROM document_versions newer
     WHERE newer.target_kind=d.target_kind AND newer.target_id=d.target_id AND newer.target_scope=d.target_scope
      AND newer.document_kind=d.document_kind AND newer.document_id=d.document_id
      AND newer.revision>d.revision AND newer.revision<=a.current_revision))`
}

func (s *Store) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
 SELECT a.target_id,
  (SELECT d.content FROM document_versions d
   WHERE d.target_kind=a.target_kind AND d.target_id=a.target_id AND d.target_scope=''
    AND d.document_kind=? AND d.document_id=? AND d.revision<=a.current_revision
   ORDER BY d.revision DESC LIMIT 1),
  (SELECT CASE WHEN d.operation='put' THEN d.content END FROM document_versions d
   WHERE d.target_kind=a.target_kind AND d.target_id=a.target_id AND d.target_scope=''
    AND d.document_kind=? AND d.document_id=? AND d.revision<=a.current_revision
   ORDER BY d.revision DESC LIMIT 1),
  `+liveCount(`
    AND json_extract(CAST(d.content AS TEXT),'$.kind')=?`)+`,
  `+liveCount("")+`
 FROM authority_streams a
 WHERE a.target_kind=? AND a.target_scope='' AND a.current_revision>0
 ORDER BY a.target_id`,
		model.DocumentIntent, model.SingletonDocumentID, model.DocumentCompass, model.SingletonDocumentID,
		model.DocumentPlan, model.PlanChapter, model.DocumentManuscript, model.AuthorityProject)
	if err != nil {
		return nil, fmt.Errorf("list project summaries: %w", err)
	}
	defer rows.Close()
	var result []ProjectSummary
	for rows.Next() {
		var summary ProjectSummary
		var compass []byte
		if err := rows.Scan(&summary.ID, &summary.Intent, &compass, &summary.Planned, &summary.Written); err != nil {
			return nil, fmt.Errorf("scan project summary: %w", err)
		}
		summary.Compass = compass
		result = append(result, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project summaries: %w", err)
	}
	return result, nil
}
