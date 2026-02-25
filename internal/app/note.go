package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

const (
	notebookShapingSkillName = "notebook-shaping"
	defaultScopeEstimate     = "M"
	defaultTitleMaxLength    = 80
	maxPMNotesRunes          = 1000
	defaultPMNotesFallback   = "notebook shaping skill loaded"
)

var defaultAcceptanceItems = []string{
	"功能实现完整",
	"测试覆盖关键路径",
	"文档已更新",
}

type notebookShapingRules struct {
	ScopeEstimate   string
	AcceptanceItems []string
	TitleMaxLength  int
	StripMarkdown   bool
	PMNotes         string
	ParseWarning    string
}

func (p *Project) AddNote(ctx context.Context, rawText string) (NoteAddResult, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return NoteAddResult{}, fmt.Errorf("project db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return NoteAddResult{}, fmt.Errorf("note 文本不能为空")
	}
	normalized := normalizeNoteText(rawText)
	nHash := hashNormalizedText(normalized)
	projectKey := strings.TrimSpace(p.Key())

	result := NoteAddResult{}
	err := p.core.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing store.NoteItem
		err := tx.WithContext(ctx).
			Where("(project_key = ? OR project_key = '') AND normalized_hash = ? AND status IN ?", projectKey, nHash, []store.NoteStatus{store.NoteOpen, store.NoteShaping}).
			Order("id desc").
			First(&existing).Error
		if err == nil {
			if strings.TrimSpace(existing.ProjectKey) == "" && projectKey != "" {
				_ = tx.WithContext(ctx).
					Model(&store.NoteItem{}).
					Where("id = ?", existing.ID).
					Update("project_key", projectKey).Error
			}
			result = NoteAddResult{
				NoteID:       existing.ID,
				ShapedItemID: existing.ShapedItemID,
				Deduped:      true,
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		note := store.NoteItem{
			ProjectKey:     projectKey,
			Status:         store.NoteOpen,
			Source:         "cli",
			Text:           rawText,
			ContextJSON:    "",
			NormalizedHash: nHash,
			ShapedItemID:   0,
			LastError:      "",
		}
		if err := tx.WithContext(ctx).Create(&note).Error; err != nil {
			return err
		}
		result = NoteAddResult{
			NoteID:       note.ID,
			ShapedItemID: 0,
			Deduped:      false,
		}
		return nil
	})
	if err != nil {
		return NoteAddResult{}, err
	}
	return result, nil
}

func (p *Project) NotebookShapingSkillPath() string {
	if p == nil || p.core == nil {
		return ""
	}
	base := strings.TrimSpace(p.core.Layout.ControlSkillsDir)
	if base == "" {
		return ""
	}
	return filepath.Join(base, notebookShapingSkillName, "SKILL.md")
}

func (p *Project) ProcessOnePendingNote(ctx context.Context) (bool, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return false, fmt.Errorf("project db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	note, err := p.claimOneOpenNote(ctx)
	if err != nil {
		return false, err
	}
	if note == nil {
		return false, nil
	}
	if err := p.shapeClaimedNote(ctx, *note); err != nil {
		return true, err
	}
	return true, nil
}

func (p *Project) RecoverStuckShapingNotes(ctx context.Context, stale time.Duration) (int, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return 0, fmt.Errorf("project db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if stale <= 0 {
		stale = 5 * time.Minute
	}
	cutoff := time.Now().Add(-stale)
	projectKey := strings.TrimSpace(p.Key())
	var notes []store.NoteItem
	if err := p.core.DB.WithContext(ctx).
		Where("(project_key = ? OR project_key = '') AND status = ? AND updated_at < ?", projectKey, store.NoteShaping, cutoff).
		Order("id asc").
		Find(&notes).Error; err != nil {
		return 0, err
	}
	if len(notes) == 0 {
		return 0, nil
	}
	now := time.Now()
	for _, note := range notes {
		msg := "daemon recovery: shaping interrupted, rolled back to open"
		if err := p.core.DB.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ?", note.ID).
			Updates(map[string]any{
				"status":     store.NoteOpen,
				"last_error": msg,
				"updated_at": now,
			}).Error; err != nil {
			continue
		}
		_ = p.upsertNoteInbox(ctx, fmt.Sprintf("note_recovery_%d", note.ID), fmt.Sprintf("note %d shaping 中断，已回滚", note.ID), msg, 0)
	}
	return len(notes), nil
}

func (p *Project) claimOneOpenNote(ctx context.Context) (*store.NoteItem, error) {
	var claimed *store.NoteItem
	projectKey := strings.TrimSpace(p.Key())
	err := p.core.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var note store.NoteItem
		err := tx.WithContext(ctx).
			Where("(project_key = ? OR project_key = '') AND status = ?", projectKey, store.NoteOpen).
			Order("id asc").
			First(&note).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		now := time.Now()
		res := tx.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ? AND status = ?", note.ID, store.NoteOpen).
			Updates(map[string]any{
				"project_key": projectKey,
				"status":      store.NoteShaping,
				"updated_at":  now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		note.Status = store.NoteShaping
		note.ProjectKey = projectKey
		note.UpdatedAt = now
		claimed = &note
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (p *Project) shapeClaimedNote(ctx context.Context, note store.NoteItem) error {
	if p == nil || p.core == nil || p.core.DB == nil {
		return fmt.Errorf("project db 为空")
	}
	skillPath := p.NotebookShapingSkillPath()
	skillText, err := os.ReadFile(skillPath)
	if err != nil {
		msg := fmt.Sprintf("notebook shaping skill 缺失或不可读: %s", skillPath)
		_ = p.core.DB.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ?", note.ID).
			Updates(map[string]any{
				"status":     store.NoteOpen,
				"last_error": msg,
				"updated_at": time.Now(),
			}).Error
		_ = p.upsertNoteInbox(ctx,
			fmt.Sprintf("note_skill_missing_%d", note.ID),
			"Notebook shaping skill 缺失",
			fmt.Sprintf("note=%d\n缺少文件: %s\n建议: 执行 dalek init 重新播种 control 后重试", note.ID, skillPath),
			note.ID,
		)
		return nil
	}

	dedupKey := dedupKeyFromNormalizedHash(note.NormalizedHash)
	projectKey := strings.TrimSpace(note.ProjectKey)
	if projectKey == "" {
		projectKey = strings.TrimSpace(p.Key())
	}
	rules := parseNotebookShapingRules(string(skillText))
	title := buildShapedTitle(note.Text, rules)
	desc := strings.TrimSpace(note.Text)
	if desc == "" {
		desc = title
	}
	notes := strings.TrimSpace(rules.PMNotes)
	if notes == "" {
		notes = defaultPMNotesFallback
	}
	acceptanceJSON := marshalAcceptanceItems(rules.AcceptanceItems)
	scopeEstimate := strings.TrimSpace(rules.ScopeEstimate)
	if scopeEstimate == "" {
		scopeEstimate = defaultScopeEstimate
	}

	return p.core.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var latest store.NoteItem
		if err := tx.WithContext(ctx).First(&latest, note.ID).Error; err != nil {
			return err
		}
		if latest.Status != store.NoteShaping {
			return nil
		}

		var shaped store.ShapedItem
		err := tx.WithContext(ctx).
			Where("project_key = ? AND dedup_key = ?", projectKey, dedupKey).
			Order("id desc").
			First(&shaped).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		now := time.Now()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			shaped = store.ShapedItem{
				ProjectKey:     projectKey,
				Status:         store.ShapedPendingReview,
				Title:          title,
				Description:    desc,
				AcceptanceJSON: acceptanceJSON,
				PMNotes:        notes,
				ScopeEstimate:  scopeEstimate,
				DedupKey:       dedupKey,
				SourceNoteIDs:  defaultSourceNoteIDs(note.ID),
			}
			if err := tx.WithContext(ctx).Create(&shaped).Error; err != nil {
				return err
			}
		} else {
			merged := mergeSourceNoteIDs(shaped.SourceNoteIDs, note.ID)
			if err := tx.WithContext(ctx).
				Model(&store.ShapedItem{}).
				Where("id = ?", shaped.ID).
				Updates(map[string]any{
					"source_note_ids": merged,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
		}

		if err := tx.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ?", note.ID).
			Updates(map[string]any{
				"project_key":    projectKey,
				"status":         store.NoteShaped,
				"shaped_item_id": shaped.ID,
				"last_error":     "",
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (p *Project) upsertNoteInbox(ctx context.Context, key, title, body string, noteID uint) error {
	if p == nil || p.core == nil || p.core.DB == nil {
		return fmt.Errorf("project db 为空")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = fmt.Sprintf("note_inbox_%d", noteID)
	}
	body = strings.TrimSpace(body)
	now := time.Now()

	var existing store.InboxItem
	err := p.core.DB.WithContext(ctx).
		Where("key = ? AND status = ?", key, store.InboxOpen).
		Order("id desc").
		First(&existing).Error
	if err == nil {
		return p.core.DB.WithContext(ctx).
			Model(&store.InboxItem{}).
			Where("id = ?", existing.ID).
			Updates(map[string]any{
				"title":      strings.TrimSpace(title),
				"body":       body,
				"updated_at": now,
			}).Error
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	item := store.InboxItem{
		Key:      key,
		Status:   store.InboxOpen,
		Severity: store.InboxWarn,
		Reason:   store.InboxIncident,
		Title:    strings.TrimSpace(title),
		Body:     body,
	}
	return p.core.DB.WithContext(ctx).Create(&item).Error
}

func (p *Project) ListNotes(ctx context.Context, opt ListNoteOptions) ([]NoteView, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	q := p.core.DB.WithContext(ctx).Model(&store.NoteItem{})
	if statusOnly := strings.TrimSpace(strings.ToLower(opt.StatusOnly)); statusOnly != "" {
		if statusOnly == string(store.NoteShaped) || statusOnly == string(store.NotePendingReviewLegacy) {
			q = q.Where("status IN ?", []store.NoteStatus{store.NoteShaped, store.NotePendingReviewLegacy})
		} else {
			q = q.Where("status = ?", statusOnly)
		}
	}
	if opt.ShapedOnly {
		q = q.Where("shaped_item_id > 0")
	}

	var notes []store.NoteItem
	if err := q.Order("id desc").Limit(limit).Find(&notes).Error; err != nil {
		return nil, err
	}
	return p.buildNoteViews(ctx, notes)
}

func (p *Project) GetNote(ctx context.Context, id uint) (*NoteView, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	if id == 0 {
		return nil, fmt.Errorf("note id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var note store.NoteItem
	if err := p.core.DB.WithContext(ctx).First(&note, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	views, err := p.buildNoteViews(ctx, []store.NoteItem{note})
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, nil
	}
	v := views[0]
	return &v, nil
}

func (p *Project) ApproveNote(ctx context.Context, id uint, reviewedBy string) (*store.Ticket, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	if id == 0 {
		return nil, fmt.Errorf("note id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reviewedBy = strings.TrimSpace(reviewedBy)
	if reviewedBy == "" {
		reviewedBy = "cli"
	}

	var outTicket store.Ticket
	err := p.core.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var note store.NoteItem
		if err := tx.WithContext(ctx).First(&note, id).Error; err != nil {
			return err
		}
		switch {
		case isLegacyNoteApproved(note.Status):
			return fmt.Errorf("note 已审批")
		case note.Status == store.NoteDiscarded:
			return fmt.Errorf("note 已丢弃，不能审批")
		case note.Status == store.NoteOpen || note.Status == store.NoteShaping:
			return fmt.Errorf("note 尚未 shaping 完成，请稍后重试")
		case !isNoteShaped(note.Status):
			return fmt.Errorf("note 状态不支持审批: %s", strings.TrimSpace(string(note.Status)))
		}
		if note.ShapedItemID == 0 {
			return fmt.Errorf("note 尚未生成 shaped item，不能审批")
		}

		var shaped store.ShapedItem
		if err := tx.WithContext(ctx).First(&shaped, note.ShapedItemID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("shaped item 不存在，不能审批")
			}
			return err
		}
		if shaped.Status == store.ShapedApproved {
			if shaped.TicketID != 0 {
				if err := tx.WithContext(ctx).First(&outTicket, shaped.TicketID).Error; err == nil {
					return nil
				}
			}
			return fmt.Errorf("note 已审批")
		}

		title := strings.TrimSpace(shaped.Title)
		if title == "" {
			title = defaultShapedTitle(note.Text)
		}
		desc := strings.TrimSpace(shaped.Description)
		if desc == "" {
			desc = strings.TrimSpace(note.Text)
		}
		if strings.TrimSpace(desc) == "" {
			desc = title
		}

		now := time.Now()
		ticket := store.Ticket{
			Title:          trimOneLineNote(title),
			Description:    strings.TrimSpace(desc),
			WorkflowStatus: store.TicketBacklog,
			Priority:       0,
		}
		if ticket.Title == "" {
			ticket.Title = "未命名需求"
		}
		if ticket.Description == "" {
			ticket.Description = ticket.Title
		}
		if err := tx.WithContext(ctx).Create(&ticket).Error; err != nil {
			return err
		}

		if err := tx.WithContext(ctx).
			Model(&store.ShapedItem{}).
			Where("id = ?", note.ShapedItemID).
			Updates(map[string]any{
				"status":         store.ShapedApproved,
				"ticket_id":      ticket.ID,
				"reviewed_at":    now,
				"reviewed_by":    reviewedBy,
				"review_comment": "",
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}

		if err := tx.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ?", note.ID).
			Updates(map[string]any{
				"status":     store.NoteShaped,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		outTicket = ticket
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &outTicket, nil
}

func (p *Project) RejectNote(ctx context.Context, id uint, reason string) error {
	if p == nil || p.core == nil || p.core.DB == nil {
		return fmt.Errorf("project db 为空")
	}
	if id == 0 {
		return fmt.Errorf("note id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "rejected"
	}
	now := time.Now()

	return p.core.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var note store.NoteItem
		if err := tx.WithContext(ctx).First(&note, id).Error; err != nil {
			return err
		}
		switch {
		case note.Status == store.NoteDiscarded:
			return fmt.Errorf("note 已丢弃，不能驳回")
		case note.Status == store.NoteOpen || note.Status == store.NoteShaping:
			return fmt.Errorf("note 尚未 shaping 完成，请稍后重试")
		case isLegacyNoteApproved(note.Status):
			return fmt.Errorf("note 已审批，不能驳回")
		case !isNoteShaped(note.Status) && !isLegacyNoteRejected(note.Status):
			return fmt.Errorf("note 状态不支持驳回: %s", strings.TrimSpace(string(note.Status)))
		}
		if note.ShapedItemID == 0 {
			return fmt.Errorf("note 尚未生成 shaped item，不能驳回")
		}

		var shaped store.ShapedItem
		if err := tx.WithContext(ctx).First(&shaped, note.ShapedItemID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("shaped item 不存在，不能驳回")
			}
			return err
		}
		if shaped.Status == store.ShapedApproved {
			return fmt.Errorf("note 已审批，不能驳回")
		}

		if err := tx.WithContext(ctx).
			Model(&store.NoteItem{}).
			Where("id = ?", note.ID).
			Updates(map[string]any{
				"status":     store.NoteShaped,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).
			Model(&store.ShapedItem{}).
			Where("id = ?", note.ShapedItemID).
			Updates(map[string]any{
				"status":         store.ShapedRejected,
				"review_comment": reason,
				"reviewed_at":    now,
				"reviewed_by":    "cli",
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (p *Project) DiscardNote(ctx context.Context, id uint) error {
	if p == nil || p.core == nil || p.core.DB == nil {
		return fmt.Errorf("project db 为空")
	}
	if id == 0 {
		return fmt.Errorf("note id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return p.core.DB.WithContext(ctx).
		Model(&store.NoteItem{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     store.NoteDiscarded,
			"updated_at": now,
		}).Error
}

func (p *Project) buildNoteViews(ctx context.Context, notes []store.NoteItem) ([]NoteView, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	if len(notes) == 0 {
		return []NoteView{}, nil
	}
	ids := make([]uint, 0, len(notes))
	for _, n := range notes {
		if n.ShapedItemID != 0 {
			ids = append(ids, n.ShapedItemID)
		}
	}
	shapedMap := map[uint]store.ShapedItem{}
	if len(ids) > 0 {
		var shaped []store.ShapedItem
		if err := p.core.DB.WithContext(ctx).Where("id IN ?", ids).Find(&shaped).Error; err != nil {
			return nil, err
		}
		for _, s := range shaped {
			shapedMap[s.ID] = s
		}
	}

	out := make([]NoteView, 0, len(notes))
	for _, n := range notes {
		projectKey := strings.TrimSpace(n.ProjectKey)
		if projectKey == "" {
			projectKey = strings.TrimSpace(p.Key())
		}
		view := NoteView{
			ProjectKey:     projectKey,
			ID:             n.ID,
			Status:         string(canonicalNoteStatus(n.Status)),
			Text:           strings.TrimSpace(n.Text),
			ContextJSON:    strings.TrimSpace(n.ContextJSON),
			NormalizedHash: strings.TrimSpace(n.NormalizedHash),
			ShapedItemID:   n.ShapedItemID,
			LastError:      strings.TrimSpace(n.LastError),
			CreatedAt:      n.CreatedAt,
			UpdatedAt:      n.UpdatedAt,
		}
		if s, ok := shapedMap[n.ShapedItemID]; ok {
			shapedProjectKey := strings.TrimSpace(s.ProjectKey)
			if shapedProjectKey == "" {
				shapedProjectKey = projectKey
			}
			view.Shaped = &ShapedView{
				ID:             s.ID,
				ProjectKey:     shapedProjectKey,
				Status:         string(s.Status),
				Title:          strings.TrimSpace(s.Title),
				Description:    strings.TrimSpace(s.Description),
				AcceptanceJSON: strings.TrimSpace(s.AcceptanceJSON),
				PMNotes:        strings.TrimSpace(s.PMNotes),
				ScopeEstimate:  strings.TrimSpace(s.ScopeEstimate),
				DedupKey:       strings.TrimSpace(s.DedupKey),
				SourceNoteIDs:  strings.TrimSpace(s.SourceNoteIDs),
				TicketID:       s.TicketID,
				ReviewComment:  strings.TrimSpace(s.ReviewComment),
				ReviewedAt:     s.ReviewedAt,
				ReviewedBy:     strings.TrimSpace(s.ReviewedBy),
				CreatedAt:      s.CreatedAt,
				UpdatedAt:      s.UpdatedAt,
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func normalizeNoteText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	parts := strings.Fields(s)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func hashNormalizedText(s string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}

func dedupKeyFromNormalizedHash(hash string) string {
	hash = strings.TrimSpace(strings.ToLower(hash))
	if hash == "" {
		hash = hashNormalizedText(fmt.Sprintf("fallback_%d", time.Now().UnixNano()))
	}
	if len(hash) > 24 {
		hash = hash[:24]
	}
	return "note_" + hash
}

func parseNotebookShapingRules(raw string) notebookShapingRules {
	rules := notebookShapingRules{
		ScopeEstimate:   defaultScopeEstimate,
		AcceptanceItems: append([]string{}, defaultAcceptanceItems...),
		TitleMaxLength:  defaultTitleMaxLength,
		StripMarkdown:   true,
		PMNotes:         defaultPMNotesFallback,
	}

	frontMatter, body, hasFrontMatter, err := splitFrontMatter(raw)
	if err != nil {
		rules.ParseWarning = err.Error()
	} else if hasFrontMatter {
		if err := applyNotebookShapingFrontMatter(&rules, frontMatter); err != nil {
			rules.ParseWarning = err.Error()
		}
	}

	body = strings.TrimSpace(body)
	if body == "" {
		body = strings.TrimSpace(firstNonEmptyLine(raw))
	}
	if body == "" {
		body = defaultPMNotesFallback
	}
	if rules.ParseWarning != "" {
		body = "SKILL.md front matter 解析失败，已回退默认规则: " + rules.ParseWarning + "\n\n" + body
	}
	rules.PMNotes = truncateRunes(strings.TrimSpace(body), maxPMNotesRunes)
	if rules.PMNotes == "" {
		rules.PMNotes = defaultPMNotesFallback
	}
	if rules.TitleMaxLength <= 0 {
		rules.TitleMaxLength = defaultTitleMaxLength
	}
	rules.ScopeEstimate = strings.TrimSpace(rules.ScopeEstimate)
	if rules.ScopeEstimate == "" {
		rules.ScopeEstimate = defaultScopeEstimate
	}
	if len(rules.AcceptanceItems) == 0 {
		rules.AcceptanceItems = append([]string{}, defaultAcceptanceItems...)
	}
	return rules
}

func splitFrontMatter(raw string) (frontMatter string, body string, hasFrontMatter bool, err error) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", text, false, nil
	}
	hasFrontMatter = true
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end <= 0 {
		return "", text, true, fmt.Errorf("front matter 缺少结束分隔符 ---")
	}
	frontMatter = strings.Join(lines[1:end], "\n")
	if end+1 < len(lines) {
		body = strings.Join(lines[end+1:], "\n")
	}
	return frontMatter, body, true, nil
}

func applyNotebookShapingFrontMatter(rules *notebookShapingRules, frontMatter string) error {
	if rules == nil {
		return fmt.Errorf("rules 不能为空")
	}
	lines := strings.Split(strings.ReplaceAll(frontMatter, "\r", "\n"), "\n")
	section := ""
	for i := 0; i < len(lines); {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}

		indent := leadingWhitespaceWidth(line)
		if indent == 0 && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			i++
			continue
		}
		if indent < 2 {
			i++
			continue
		}

		key, value, ok := splitFrontMatterKeyValue(trimmed)
		if !ok {
			i++
			continue
		}
		switch section {
		case "defaults":
			switch key {
			case "scope_estimate":
				if v := parseFrontMatterScalar(value); v != "" {
					rules.ScopeEstimate = v
				}
			case "acceptance_template":
				if isBlockScalar(value) {
					block := make([]string, 0, 8)
					i++
					for i < len(lines) {
						nextLine := strings.TrimRight(lines[i], " \t")
						if strings.TrimSpace(nextLine) == "" {
							block = append(block, "")
							i++
							continue
						}
						if leadingWhitespaceWidth(nextLine) < 4 {
							break
						}
						block = append(block, strings.TrimSpace(nextLine))
						i++
					}
					if items := parseAcceptanceTemplate(strings.Join(block, "\n")); len(items) > 0 {
						rules.AcceptanceItems = items
					}
					continue
				}
				if items := parseAcceptanceTemplate(parseFrontMatterScalar(value)); len(items) > 0 {
					rules.AcceptanceItems = items
				}
			}
		case "title_rules":
			switch key {
			case "max_length":
				if n, err := strconv.Atoi(parseFrontMatterScalar(value)); err == nil && n > 0 {
					rules.TitleMaxLength = n
				}
			case "strip_markdown":
				if b, ok := parseFrontMatterBool(value); ok {
					rules.StripMarkdown = b
				}
			}
		}
		i++
	}
	return nil
}

func buildShapedTitle(raw string, rules notebookShapingRules) string {
	title := defaultShapedTitle(raw)
	if rules.StripMarkdown {
		title = stripMarkdownDecorators(title)
	}
	if title == "" {
		title = defaultShapedTitle(raw)
	}
	title = truncateRunes(strings.TrimSpace(title), rules.TitleMaxLength)
	if title == "" {
		return "未命名需求"
	}
	return title
}

func marshalAcceptanceItems(items []string) string {
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		cleaned = append(cleaned, item)
	}
	if len(cleaned) == 0 {
		cleaned = append(cleaned, defaultAcceptanceItems...)
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func parseAcceptanceTemplate(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- [ ]"):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [ ]"))
		case strings.HasPrefix(line, "- [x]"):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [x]"))
		case strings.HasPrefix(line, "- [X]"):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [X]"))
		case strings.HasPrefix(line, "-"):
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		}
		line = strings.TrimSpace(line)
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

func splitFrontMatterKeyValue(line string) (key string, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func parseFrontMatterScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
			return strings.TrimSpace(raw[1 : len(raw)-1])
		}
		if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
			return strings.TrimSpace(raw[1 : len(raw)-1])
		}
	}
	return strings.TrimSpace(raw)
}

func parseFrontMatterBool(raw string) (bool, bool) {
	switch strings.ToLower(parseFrontMatterScalar(raw)) {
	case "true", "yes", "on":
		return true, true
	case "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func isBlockScalar(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "|", "|-", "|+", ">", ">-", ">+":
		return true
	default:
		return false
	}
}

func leadingWhitespaceWidth(line string) int {
	w := 0
	for _, r := range line {
		if r == ' ' {
			w++
			continue
		}
		if r == '\t' {
			w += 2
			continue
		}
		break
	}
	return w
}

func stripMarkdownDecorators(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for {
		trimmed := strings.TrimSpace(strings.TrimLeft(s, "#>*+-` "))
		if trimmed == s {
			break
		}
		s = trimmed
	}
	replacer := strings.NewReplacer(
		"**", "",
		"__", "",
		"`", "",
		"[", "",
		"]", "",
	)
	return strings.TrimSpace(replacer.Replace(s))
}

func truncateRunes(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 {
		limit = defaultTitleMaxLength
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func isNoteShaped(status store.NoteStatus) bool {
	switch canonicalNoteStatus(status) {
	case store.NoteShaped:
		return true
	default:
		return false
	}
}

func canonicalNoteStatus(status store.NoteStatus) store.NoteStatus {
	switch {
	case status == store.NoteShaped:
		return store.NoteShaped
	case status == store.NotePendingReviewLegacy:
		return store.NoteShaped
	case isLegacyNoteApproved(status):
		return store.NoteShaped
	case isLegacyNoteRejected(status):
		return store.NoteShaped
	default:
		return status
	}
}

func isLegacyNoteApproved(status store.NoteStatus) bool {
	return strings.EqualFold(strings.TrimSpace(string(status)), "approved")
}

func isLegacyNoteRejected(status store.NoteStatus) bool {
	return strings.EqualFold(strings.TrimSpace(string(status)), "rejected")
}

func defaultShapedTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "未命名需求"
	}
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	title := strings.TrimSpace(lines[0])
	if title == "" {
		title = strings.TrimSpace(raw)
	}
	title = truncateRunes(title, defaultTitleMaxLength)
	if title == "" {
		return "未命名需求"
	}
	return title
}

func firstNonEmptyLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "\n")
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			return ln
		}
	}
	return ""
}

func defaultSourceNoteIDs(noteID uint) string {
	if noteID == 0 {
		return "[]"
	}
	b, err := json.Marshal([]uint{noteID})
	if err != nil {
		return "[]"
	}
	return string(b)
}

func mergeSourceNoteIDs(raw string, id uint) string {
	if id == 0 {
		if strings.TrimSpace(raw) == "" {
			return "[]"
		}
		return strings.TrimSpace(raw)
	}
	var ids []uint
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	set := map[uint]bool{}
	for _, v := range ids {
		if v != 0 {
			set[v] = true
		}
	}
	set[id] = true
	out := make([]uint, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func trimOneLineNote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
