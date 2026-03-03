package feishudoc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

type CreateDocumentInput struct {
	Title       string
	FolderToken string
}

type Document struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	RevisionID int    `json:"revision_id,omitempty"`
	URL        string `json:"url,omitempty"`
}

type ReadDocumentOptions struct {
	ImagesDir       string
	ImagePathPrefix string
}

type ReadDocumentResult struct {
	Document Document `json:"document"`
	Content  string   `json:"content"`
	Warnings []string `json:"warnings,omitempty"`
}

type WriteDocumentInput struct {
	DocumentID string
	Content    string
}

type WriteDocumentResult struct {
	DocumentID       string   `json:"document_id"`
	DocumentRevision int      `json:"document_revision"`
	AddedBlockIDs    []string `json:"added_block_ids"`
}

type ListDocumentsInput struct {
	FolderToken string
	PageToken   string
	PageSize    int
}

type ListDocumentsResult struct {
	Documents     []Document `json:"documents"`
	HasMore       bool       `json:"has_more"`
	NextPageToken string     `json:"next_page_token,omitempty"`
}

const (
	blockTypePage           = 1
	blockTypeText           = 2
	blockTypeHeading1       = 3
	blockTypeHeading9       = 11
	blockTypeBullet         = 12
	blockTypeOrdered        = 13
	blockTypeCode           = 14
	blockTypeQuote          = 15
	blockTypeEquation       = 16
	blockTypeTodo           = 17
	blockTypeDivider        = 22
	blockTypeImage          = 27
	blockTypeTableCell      = 28
	blockTypeTable          = 31
	blockTypeQuoteContainer = 34
	blockTypeCallout        = 40
)

const listBlockPageSize = 500
const mediaBatchSize = 50

func (s *Service) CreateDocument(ctx context.Context, input CreateDocumentInput) (*Document, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	bodyBuilder := larkdocx.NewCreateDocumentReqBodyBuilder()
	title := strings.TrimSpace(input.Title)
	if title != "" {
		bodyBuilder.Title(title)
	}
	folderToken := strings.TrimSpace(input.FolderToken)
	if folderToken != "" {
		bodyBuilder.FolderToken(folderToken)
	}

	resp, err := s.client.Docx.V1.Document.Create(ctx,
		larkdocx.NewCreateDocumentReqBuilder().
			Body(bodyBuilder.Build()).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("创建文档失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("创建文档失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("创建文档失败", resp.Code, resp.Msg, logID)
	}
	if resp.Data == nil || resp.Data.Document == nil {
		return nil, fmt.Errorf("创建文档失败: 缺少 document 数据")
	}

	docID := stringValue(resp.Data.Document.DocumentId)
	if docID == "" {
		return nil, fmt.Errorf("创建文档失败: document_id 为空")
	}
	return &Document{
		DocumentID: docID,
		Title:      stringValue(resp.Data.Document.Title),
		RevisionID: intValue(resp.Data.Document.RevisionId),
		URL:        s.documentURL(docID),
	}, nil
}

func (s *Service) ReadDocument(ctx context.Context, documentID string, options ...ReadDocumentOptions) (*ReadDocumentResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return nil, fmt.Errorf("document_id 不能为空")
	}

	readOpts := ReadDocumentOptions{}
	if len(options) > 0 {
		readOpts = options[0]
	}
	readOpts = normalizeReadDocumentOptions(readOpts)

	getResp, err := s.client.Docx.V1.Document.Get(ctx,
		larkdocx.NewGetDocumentReqBuilder().
			DocumentId(documentID).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("读取文档元信息失败: %w", err)
	}
	if getResp == nil {
		return nil, fmt.Errorf("读取文档元信息失败: 响应为空")
	}
	if !getResp.Success() {
		logID := ""
		if getResp.Err != nil {
			logID = strings.TrimSpace(getResp.Err.LogID)
		}
		return nil, newCodeError("读取文档元信息失败", getResp.Code, getResp.Msg, logID)
	}

	blocks, err := s.fetchAllBlocks(ctx, documentID)
	if err != nil {
		return nil, err
	}

	roots, nodeByID := buildDocumentBlockTree(documentID, blocks)
	imageTokens := collectImageTokens(blocks)
	imageRefs := make(map[string]string, len(imageTokens))
	warnings := make([]string, 0)

	if len(imageTokens) > 0 {
		if readOpts.ImagesDir != "" {
			localRefs, localWarnings, err := s.downloadImages(ctx, imageTokens, readOpts.ImagesDir, readOpts.ImagePathPrefix)
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, localWarnings...)
			for token, ref := range localRefs {
				imageRefs[token] = ref
			}
		}

		missingTokens := missingImageTokens(imageTokens, imageRefs)
		if len(missingTokens) > 0 {
			tmpRefs, tmpWarnings := s.fetchImageTmpDownloadURLs(ctx, missingTokens)
			warnings = append(warnings, tmpWarnings...)
			for token, ref := range tmpRefs {
				if imageRefs[token] == "" {
					imageRefs[token] = ref
				}
			}
		}
	}

	renderer := newDocMarkdownRenderer(nodeByID, imageRefs)
	content := strings.TrimSpace(renderer.render(roots))
	if content != "" {
		content += "\n"
	}

	title := ""
	revision := 0
	if getResp.Data != nil && getResp.Data.Document != nil {
		title = stringValue(getResp.Data.Document.Title)
		revision = intValue(getResp.Data.Document.RevisionId)
	}

	return &ReadDocumentResult{
		Document: Document{
			DocumentID: documentID,
			Title:      title,
			RevisionID: revision,
			URL:        s.documentURL(documentID),
		},
		Content:  content,
		Warnings: warnings,
	}, nil
}

func normalizeReadDocumentOptions(options ReadDocumentOptions) ReadDocumentOptions {
	options.ImagesDir = strings.TrimSpace(options.ImagesDir)
	options.ImagePathPrefix = normalizeImagePathPrefix(options.ImagePathPrefix)
	return options
}

func normalizeImagePathPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "./images"
	}
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return "./images"
	}
	return prefix
}

func (s *Service) fetchAllBlocks(ctx context.Context, documentID string) ([]*larkdocx.Block, error) {
	items := make([]*larkdocx.Block, 0)
	pageToken := ""

	for {
		req := larkdocx.NewListDocumentBlockReqBuilder().
			DocumentId(documentID).
			PageSize(listBlockPageSize)
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		resp, err := s.client.Docx.V1.DocumentBlock.List(ctx, req.Build())
		if err != nil {
			return nil, fmt.Errorf("读取文档 block 失败: %w", err)
		}
		if resp == nil {
			return nil, fmt.Errorf("读取文档 block 失败: 响应为空")
		}
		if !resp.Success() {
			logID := ""
			if resp.Err != nil {
				logID = strings.TrimSpace(resp.Err.LogID)
			}
			return nil, newCodeError("读取文档 block 失败", resp.Code, resp.Msg, logID)
		}

		if resp.Data == nil {
			break
		}
		if len(resp.Data.Items) > 0 {
			items = append(items, resp.Data.Items...)
		}
		if !boolValue(resp.Data.HasMore) {
			break
		}
		next := stringValue(resp.Data.PageToken)
		if next == "" || next == pageToken {
			break
		}
		pageToken = next
	}

	return items, nil
}

func collectImageTokens(blocks []*larkdocx.Block) []string {
	seen := make(map[string]struct{})
	tokens := make([]string, 0)
	for _, block := range blocks {
		if block == nil || block.Image == nil {
			continue
		}
		token := stringValue(block.Image.Token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func missingImageTokens(allTokens []string, refs map[string]string) []string {
	missing := make([]string, 0)
	for _, token := range allTokens {
		if strings.TrimSpace(token) == "" {
			continue
		}
		if strings.TrimSpace(refs[token]) == "" {
			missing = append(missing, token)
		}
	}
	return missing
}

type docBlockNode struct {
	Block    *larkdocx.Block
	Parent   *docBlockNode
	Children []*docBlockNode
}

func buildDocumentBlockTree(documentID string, blocks []*larkdocx.Block) ([]*docBlockNode, map[string]*docBlockNode) {
	nodeByID := make(map[string]*docBlockNode, len(blocks))
	order := make([]string, 0, len(blocks))

	for _, block := range blocks {
		if block == nil {
			continue
		}
		blockID := stringValue(block.BlockId)
		if blockID == "" {
			continue
		}
		if _, exists := nodeByID[blockID]; !exists {
			nodeByID[blockID] = &docBlockNode{Block: block}
			order = append(order, blockID)
			continue
		}
		nodeByID[blockID].Block = block
	}

	for _, blockID := range order {
		node := nodeByID[blockID]
		if node == nil || node.Block == nil {
			continue
		}
		for _, childIDRaw := range node.Block.Children {
			childID := strings.TrimSpace(childIDRaw)
			if childID == "" {
				continue
			}
			child := nodeByID[childID]
			if child == nil {
				continue
			}
			if child.Parent == nil {
				child.Parent = node
			}
			node.Children = append(node.Children, child)
		}
	}

	rootID := strings.TrimSpace(documentID)
	if rootID != "" {
		if rootNode := nodeByID[rootID]; rootNode != nil && len(rootNode.Children) > 0 {
			return append([]*docBlockNode(nil), rootNode.Children...), nodeByID
		}
	}

	roots := make([]*docBlockNode, 0)
	for _, blockID := range order {
		if blockID == rootID {
			continue
		}
		node := nodeByID[blockID]
		if node == nil || node.Block == nil {
			continue
		}
		parentID := stringValue(node.Block.ParentId)
		if parentID == "" || parentID == rootID || nodeByID[parentID] == nil {
			roots = append(roots, node)
		}
	}

	return roots, nodeByID
}

type docMarkdownRenderer struct {
	nodeByID map[string]*docBlockNode
	imageRef map[string]string
}

func newDocMarkdownRenderer(nodeByID map[string]*docBlockNode, imageRef map[string]string) *docMarkdownRenderer {
	return &docMarkdownRenderer{
		nodeByID: nodeByID,
		imageRef: imageRef,
	}
}

func (r *docMarkdownRenderer) render(nodes []*docBlockNode) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		rendered := strings.TrimSpace(r.renderNode(node, map[string]bool{}))
		if rendered == "" {
			continue
		}
		parts = append(parts, rendered)
	}
	return strings.Join(parts, "\n\n")
}

func (r *docMarkdownRenderer) renderNode(node *docBlockNode, seen map[string]bool) string {
	if node == nil || node.Block == nil {
		return ""
	}

	blockID := stringValue(node.Block.BlockId)
	if blockID != "" {
		if seen[blockID] {
			return ""
		}
		seen[blockID] = true
		defer delete(seen, blockID)
	}

	switch intValue(node.Block.BlockType) {
	case blockTypePage:
		return strings.Join(r.renderChildren(node, seen), "\n\n")
	case blockTypeBullet, blockTypeOrdered:
		return r.renderList(node, seen)
	case blockTypeQuote, blockTypeQuoteContainer:
		return r.renderQuote(node, seen)
	case blockTypeCallout:
		return r.renderCallout(node, seen)
	case blockTypeTable:
		return r.renderTable(node.Block)
	case blockTypeTableCell:
		return strings.Join(r.renderChildren(node, seen), "\n\n")
	default:
		current := strings.TrimSpace(r.renderCurrent(node.Block))
		children := r.renderChildren(node, seen)
		parts := make([]string, 0, len(children)+1)
		if current != "" {
			parts = append(parts, current)
		}
		parts = append(parts, children...)
		if len(parts) == 0 {
			return r.renderUnsupported(node.Block)
		}
		return strings.Join(parts, "\n\n")
	}
}

func (r *docMarkdownRenderer) renderChildren(node *docBlockNode, seen map[string]bool) []string {
	parts := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		rendered := strings.TrimSpace(r.renderNode(child, seen))
		if rendered == "" {
			continue
		}
		parts = append(parts, rendered)
	}
	return parts
}

func (r *docMarkdownRenderer) renderList(node *docBlockNode, seen map[string]bool) string {
	if node == nil || node.Block == nil {
		return ""
	}

	depth := r.listDepth(node)
	indent := strings.Repeat("  ", depth)
	marker := "-"
	if intValue(node.Block.BlockType) == blockTypeOrdered {
		marker = r.orderedListMarker(node.Block)
	}

	content := strings.TrimSpace(r.renderTextBlock(node.Block))
	line := indent + marker
	if content != "" {
		line += " " + content
	}

	lines := []string{line}
	for _, child := range node.Children {
		childRendered := r.renderNode(child, seen)
		if strings.TrimSpace(childRendered) == "" {
			continue
		}
		childContent := strings.TrimRight(childRendered, "\n")
		if childContent == "" {
			continue
		}
		if child.Block != nil {
			childType := intValue(child.Block.BlockType)
			if childType == blockTypeBullet || childType == blockTypeOrdered {
				lines = append(lines, childContent)
				continue
			}
		}
		lines = append(lines, indentMultiline(strings.TrimSpace(childContent), indent+"  "))
	}

	return strings.Join(lines, "\n")
}

func (r *docMarkdownRenderer) listDepth(node *docBlockNode) int {
	depth := 0
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		blockType := intValue(parent.Block.BlockType)
		if blockType == blockTypeBullet || blockType == blockTypeOrdered {
			depth++
		}
	}
	return depth
}

func (r *docMarkdownRenderer) orderedListMarker(block *larkdocx.Block) string {
	if block == nil || block.Ordered == nil || block.Ordered.Style == nil {
		return "1."
	}
	sequence := strings.TrimSpace(stringValue(block.Ordered.Style.Sequence))
	if sequence == "" || strings.EqualFold(sequence, "auto") {
		return "1."
	}
	if _, err := strconv.Atoi(sequence); err != nil {
		return "1."
	}
	return sequence + "."
}

func (r *docMarkdownRenderer) renderQuote(node *docBlockNode, seen map[string]bool) string {
	parts := make([]string, 0, len(node.Children)+1)
	current := strings.TrimSpace(r.renderTextBlock(node.Block))
	if current != "" {
		parts = append(parts, current)
	}
	for _, child := range node.Children {
		childContent := strings.TrimSpace(r.renderNode(child, seen))
		if childContent == "" {
			continue
		}
		parts = append(parts, childContent)
	}
	if len(parts) == 0 {
		return ""
	}
	return prefixLines(strings.Join(parts, "\n\n"), "> ")
}

func (r *docMarkdownRenderer) renderCallout(node *docBlockNode, seen map[string]bool) string {
	children := r.renderChildren(node, seen)
	if len(children) == 0 {
		return "> [!NOTE]"
	}
	return "> [!NOTE]\n" + prefixLines(strings.Join(children, "\n\n"), "> ")
}

func (r *docMarkdownRenderer) renderCurrent(block *larkdocx.Block) string {
	if block == nil {
		return ""
	}

	switch blockType := intValue(block.BlockType); {
	case blockType == blockTypeText:
		return r.renderRichText(block.Text)
	case blockType >= blockTypeHeading1 && blockType <= blockTypeHeading9:
		level := blockType - blockTypeHeading1 + 1
		if level > 6 {
			level = 6
		}
		content := strings.TrimSpace(r.renderTextBlock(block))
		if content == "" {
			return ""
		}
		return strings.Repeat("#", level) + " " + content
	case blockType == blockTypeCode:
		code := r.renderPlainText(block.Code)
		if code == "" {
			return "```\n```"
		}
		return "```\n" + code + "\n```"
	case blockType == blockTypeTodo:
		done := block.Todo != nil && block.Todo.Style != nil && boolValue(block.Todo.Style.Done)
		marker := "[ ]"
		if done {
			marker = "[x]"
		}
		content := strings.TrimSpace(r.renderTextBlock(block))
		if content == "" {
			return "- " + marker
		}
		return "- " + marker + " " + content
	case blockType == blockTypeDivider:
		return "---"
	case blockType == blockTypeImage:
		return r.renderImage(block)
	case blockType == blockTypeEquation:
		equation := strings.TrimSpace(r.renderPlainText(block.Equation))
		if equation == "" {
			return ""
		}
		return "$$\n" + equation + "\n$$"
	default:
		return ""
	}
}

func (r *docMarkdownRenderer) renderTextBlock(block *larkdocx.Block) string {
	if block == nil {
		return ""
	}

	switch intValue(block.BlockType) {
	case blockTypeText:
		return r.renderRichText(block.Text)
	case blockTypeHeading1:
		return r.renderRichText(block.Heading1)
	case blockTypeHeading1 + 1:
		return r.renderRichText(block.Heading2)
	case blockTypeHeading1 + 2:
		return r.renderRichText(block.Heading3)
	case blockTypeHeading1 + 3:
		return r.renderRichText(block.Heading4)
	case blockTypeHeading1 + 4:
		return r.renderRichText(block.Heading5)
	case blockTypeHeading1 + 5:
		return r.renderRichText(block.Heading6)
	case blockTypeHeading1 + 6:
		return r.renderRichText(block.Heading7)
	case blockTypeHeading1 + 7:
		return r.renderRichText(block.Heading8)
	case blockTypeHeading1 + 8:
		return r.renderRichText(block.Heading9)
	case blockTypeBullet:
		return r.renderRichText(block.Bullet)
	case blockTypeOrdered:
		return r.renderRichText(block.Ordered)
	case blockTypeCode:
		return r.renderPlainText(block.Code)
	case blockTypeQuote:
		return r.renderRichText(block.Quote)
	case blockTypeEquation:
		return r.renderPlainText(block.Equation)
	case blockTypeTodo:
		return r.renderRichText(block.Todo)
	default:
		return ""
	}
}

func (r *docMarkdownRenderer) renderRichText(text *larkdocx.Text) string {
	if text == nil || len(text.Elements) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, element := range text.Elements {
		builder.WriteString(r.renderTextElement(element, true))
	}
	return builder.String()
}

func (r *docMarkdownRenderer) renderPlainText(text *larkdocx.Text) string {
	if text == nil || len(text.Elements) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, element := range text.Elements {
		builder.WriteString(r.renderTextElement(element, false))
	}
	return builder.String()
}

func (r *docMarkdownRenderer) renderTextElement(element *larkdocx.TextElement, styled bool) string {
	if element == nil {
		return ""
	}

	switch {
	case element.TextRun != nil:
		content := ""
		if element.TextRun.Content != nil {
			content = *element.TextRun.Content
		}
		if !styled {
			return content
		}
		return applyTextElementStyle(content, element.TextRun.TextElementStyle)
	case element.MentionUser != nil:
		text := "@" + stringValue(element.MentionUser.UserId)
		if !styled {
			return text
		}
		return applyTextElementStyle(text, element.MentionUser.TextElementStyle)
	case element.MentionDoc != nil:
		title := stringValue(element.MentionDoc.Title)
		if title == "" {
			title = stringValue(element.MentionDoc.Token)
		}
		if title == "" {
			title = "文档"
		}
		if !styled {
			return title
		}
		return applyTextElementStyle(title, element.MentionDoc.TextElementStyle)
	case element.Reminder != nil:
		text := stringValue(element.Reminder.ExpireTime)
		if text == "" {
			text = "提醒"
		}
		if !styled {
			return text
		}
		return applyTextElementStyle(text, element.Reminder.TextElementStyle)
	case element.File != nil:
		token := stringValue(element.File.FileToken)
		if token == "" {
			token = "附件"
		}
		text := "[附件:" + token + "]"
		if !styled {
			return text
		}
		return applyTextElementStyle(text, element.File.TextElementStyle)
	case element.InlineBlock != nil:
		text := "[内联块:" + stringValue(element.InlineBlock.BlockId) + "]"
		if !styled {
			return text
		}
		return applyTextElementStyle(text, element.InlineBlock.TextElementStyle)
	case element.Equation != nil:
		text := "$" + stringValue(element.Equation.Content) + "$"
		if !styled {
			return text
		}
		return applyTextElementStyle(text, element.Equation.TextElementStyle)
	case element.LinkPreview != nil:
		title := stringValue(element.LinkPreview.Title)
		url := stringValue(element.LinkPreview.Url)
		if title == "" {
			title = url
		}
		if title == "" {
			title = "链接"
		}
		if !styled {
			return title
		}
		text := applyTextElementStyle(title, element.LinkPreview.TextElementStyle)
		if url != "" {
			return "[" + text + "](" + url + ")"
		}
		return text
	default:
		return ""
	}
}

func applyTextElementStyle(content string, style *larkdocx.TextElementStyle) string {
	text := escapeMarkdownText(content)
	if style == nil {
		return text
	}

	if boolValue(style.InlineCode) {
		text = wrapInlineCode(content)
	}
	if boolValue(style.Bold) {
		text = "**" + text + "**"
	}
	if boolValue(style.Italic) {
		text = "*" + text + "*"
	}
	if boolValue(style.Strikethrough) {
		text = "~~" + text + "~~"
	}
	if boolValue(style.Underline) {
		text = "<u>" + text + "</u>"
	}
	if style.Link != nil {
		if url := stringValue(style.Link.Url); url != "" {
			text = "[" + text + "](" + url + ")"
		}
	}

	return text
}

func wrapInlineCode(content string) string {
	if strings.Contains(content, "`") {
		return "``" + strings.ReplaceAll(content, "\n", " ") + "``"
	}
	return "`" + content + "`"
}

func escapeMarkdownText(content string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"!", "\\!",
		"|", "\\|",
	)
	return replacer.Replace(content)
}

func (r *docMarkdownRenderer) renderImage(block *larkdocx.Block) string {
	if block == nil || block.Image == nil {
		return ""
	}

	token := stringValue(block.Image.Token)
	if token == "" {
		return "![image](image.png)"
	}

	alt := "image"
	if block.Image.Caption != nil {
		if caption := stringValue(block.Image.Caption.Content); caption != "" {
			alt = caption
		}
	}

	ref := strings.TrimSpace(r.imageRef[token])
	if ref == "" {
		return "![" + escapeMarkdownText(alt) + "](image.png)"
	}
	return "![" + escapeMarkdownText(alt) + "](" + ref + ")"
}

func (r *docMarkdownRenderer) renderTable(block *larkdocx.Block) string {
	if block == nil || block.Table == nil {
		return ""
	}

	rowCount := 0
	colCount := 0
	if block.Table.Property != nil {
		rowCount = intValue(block.Table.Property.RowSize)
		colCount = intValue(block.Table.Property.ColumnSize)
	}
	cells := block.Table.Cells

	switch {
	case rowCount > 0 && colCount <= 0:
		colCount = (len(cells) + rowCount - 1) / rowCount
	case colCount > 0 && rowCount <= 0:
		rowCount = (len(cells) + colCount - 1) / colCount
	case rowCount <= 0 && colCount <= 0:
		if len(cells) == 0 {
			return ""
		}
		rowCount = 1
		colCount = len(cells)
	}

	if rowCount <= 0 || colCount <= 0 {
		return ""
	}

	table := make([][]string, rowCount)
	cellIndex := 0
	for row := 0; row < rowCount; row++ {
		table[row] = make([]string, colCount)
		for col := 0; col < colCount; col++ {
			if cellIndex >= len(cells) {
				continue
			}
			table[row][col] = r.renderTableCell(cells[cellIndex])
			cellIndex++
		}
	}

	header := table[0]
	lines := make([]string, 0, rowCount+1)
	lines = append(lines, "| "+strings.Join(header, " | ")+" |")

	separator := make([]string, colCount)
	for i := range separator {
		separator[i] = "---"
	}
	lines = append(lines, "| "+strings.Join(separator, " | ")+" |")

	for row := 1; row < rowCount; row++ {
		lines = append(lines, "| "+strings.Join(table[row], " | ")+" |")
	}

	return strings.Join(lines, "\n")
}

func (r *docMarkdownRenderer) renderTableCell(cellID string) string {
	cellID = strings.TrimSpace(cellID)
	if cellID == "" {
		return ""
	}
	node := r.nodeByID[cellID]
	if node == nil {
		return ""
	}
	content := strings.TrimSpace(r.renderNode(node, map[string]bool{}))
	if content == "" {
		return ""
	}
	content = strings.ReplaceAll(content, "\n", "<br>")
	content = strings.ReplaceAll(content, "|", "\\|")
	return content
}

func (r *docMarkdownRenderer) renderUnsupported(block *larkdocx.Block) string {
	if block == nil {
		return ""
	}
	blockType := intValue(block.BlockType)
	if blockType == blockTypePage || blockType == blockTypeTableCell || blockType == blockTypeQuoteContainer {
		return ""
	}
	return fmt.Sprintf("[unsupported block: %d]", blockType)
}

func indentMultiline(content, indent string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = indent
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func prefixLines(content, prefix string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = prefix
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func (s *Service) downloadImages(ctx context.Context, tokens []string, imagesDir, pathPrefix string) (map[string]string, []string, error) {
	if strings.TrimSpace(imagesDir) == "" {
		return map[string]string{}, nil, nil
	}
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建图片目录失败: %w", err)
	}

	refs := make(map[string]string, len(tokens))
	warnings := make([]string, 0)
	for _, token := range tokens {
		resp, err := s.client.Drive.V1.Media.Download(ctx,
			larkdrive.NewDownloadMediaReqBuilder().
				FileToken(token).
				Build(),
		)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("下载图片失败(file_token=%s): %v", token, err))
			continue
		}
		if resp == nil {
			warnings = append(warnings, fmt.Sprintf("下载图片失败(file_token=%s): 响应为空", token))
			continue
		}
		if !resp.Success() {
			warnings = append(warnings, fmt.Sprintf("下载图片失败(file_token=%s): code=%d msg=%s", token, resp.Code, strings.TrimSpace(resp.Msg)))
			continue
		}

		ext := normalizeImageExtension(resp.FileName)
		fileName := sanitizeImageToken(token) + ext
		destination := filepath.Join(imagesDir, fileName)
		if err := resp.WriteFile(destination); err != nil {
			warnings = append(warnings, fmt.Sprintf("写入图片失败(file_token=%s,path=%s): %v", token, destination, err))
			continue
		}
		refs[token] = joinMarkdownPath(pathPrefix, fileName)
	}

	return refs, warnings, nil
}

func (s *Service) fetchImageTmpDownloadURLs(ctx context.Context, tokens []string) (map[string]string, []string) {
	refs := make(map[string]string, len(tokens))
	warnings := make([]string, 0)
	for start := 0; start < len(tokens); start += mediaBatchSize {
		end := start + mediaBatchSize
		if end > len(tokens) {
			end = len(tokens)
		}
		batch := tokens[start:end]

		resp, err := s.client.Drive.V1.Media.BatchGetTmpDownloadUrl(ctx,
			larkdrive.NewBatchGetTmpDownloadUrlMediaReqBuilder().
				FileTokens(batch).
				Build(),
		)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("获取图片临时链接失败(tokens=%d): %v", len(batch), err))
			continue
		}
		if resp == nil {
			warnings = append(warnings, fmt.Sprintf("获取图片临时链接失败(tokens=%d): 响应为空", len(batch)))
			continue
		}
		if !resp.Success() {
			warnings = append(warnings, fmt.Sprintf("获取图片临时链接失败(tokens=%d): code=%d msg=%s", len(batch), resp.Code, strings.TrimSpace(resp.Msg)))
			continue
		}
		if resp.Data == nil {
			continue
		}

		for _, item := range resp.Data.TmpDownloadUrls {
			if item == nil {
				continue
			}
			token := stringValue(item.FileToken)
			url := stringValue(item.TmpDownloadUrl)
			if token == "" || url == "" {
				continue
			}
			refs[token] = url
		}
	}
	return refs, warnings
}

func normalizeImageExtension(fileName string) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	if ext == "" {
		return ".png"
	}
	if len(ext) > 8 {
		return ".png"
	}
	if strings.ContainsAny(ext, "/\\") {
		return ".png"
	}
	return ext
}

func sanitizeImageToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "image"
	}
	var builder strings.Builder
	for _, ch := range token {
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
		case ch == '.', ch == '_', ch == '-':
			builder.WriteRune(ch)
		default:
			builder.WriteByte('_')
		}
	}
	safe := strings.Trim(builder.String(), "._-")
	if safe == "" {
		return "image"
	}
	return safe
}

func joinMarkdownPath(prefix, fileName string) string {
	prefix = normalizeImagePathPrefix(prefix)
	if prefix == "." {
		return "./" + fileName
	}
	return strings.TrimRight(prefix, "/") + "/" + fileName
}

func (s *Service) WriteDocument(ctx context.Context, input WriteDocumentInput) (*WriteDocumentResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	documentID := strings.TrimSpace(input.DocumentID)
	if documentID == "" {
		return nil, fmt.Errorf("document_id 不能为空")
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, fmt.Errorf("content 不能为空")
	}

	convertResp, err := s.client.Docx.V1.Document.Convert(ctx,
		larkdocx.NewConvertDocumentReqBuilder().
			Body(
				larkdocx.NewConvertDocumentReqBodyBuilder().
					ContentType(larkdocx.ContentTypeMarkdown).
					Content(content).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("转换文档内容失败: %w", err)
	}
	if convertResp == nil {
		return nil, fmt.Errorf("转换文档内容失败: 响应为空")
	}
	if !convertResp.Success() {
		logID := ""
		if convertResp.Err != nil {
			logID = strings.TrimSpace(convertResp.Err.LogID)
		}
		return nil, newCodeError("转换文档内容失败", convertResp.Code, convertResp.Msg, logID)
	}
	if convertResp.Data == nil || len(convertResp.Data.FirstLevelBlockIds) == 0 || len(convertResp.Data.Blocks) == 0 {
		return nil, fmt.Errorf("转换文档内容失败: 转换结果为空")
	}

	rootBlockResp, err := s.client.Docx.V1.DocumentBlock.Get(ctx,
		larkdocx.NewGetDocumentBlockReqBuilder().
			DocumentId(documentID).
			BlockId(documentID).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("读取文档根节点失败: %w", err)
	}
	if rootBlockResp == nil {
		return nil, fmt.Errorf("读取文档根节点失败: 响应为空")
	}
	if !rootBlockResp.Success() {
		logID := ""
		if rootBlockResp.Err != nil {
			logID = strings.TrimSpace(rootBlockResp.Err.LogID)
		}
		return nil, newCodeError("读取文档根节点失败", rootBlockResp.Code, rootBlockResp.Msg, logID)
	}

	insertIndex := 0
	if rootBlockResp.Data != nil && rootBlockResp.Data.Block != nil {
		insertIndex = len(rootBlockResp.Data.Block.Children)
	}

	writeResp, err := s.client.Docx.V1.DocumentBlockDescendant.Create(ctx,
		larkdocx.NewCreateDocumentBlockDescendantReqBuilder().
			DocumentId(documentID).
			BlockId(documentID).
			DocumentRevisionId(-1).
			Body(
				larkdocx.NewCreateDocumentBlockDescendantReqBodyBuilder().
					ChildrenId(convertResp.Data.FirstLevelBlockIds).
					Descendants(convertResp.Data.Blocks).
					Index(insertIndex).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("写入文档失败: %w", err)
	}
	if writeResp == nil {
		return nil, fmt.Errorf("写入文档失败: 响应为空")
	}
	if !writeResp.Success() {
		logID := ""
		if writeResp.Err != nil {
			logID = strings.TrimSpace(writeResp.Err.LogID)
		}
		return nil, newCodeError("写入文档失败", writeResp.Code, writeResp.Msg, logID)
	}

	added := make([]string, 0)
	revision := 0
	if writeResp.Data != nil {
		for _, block := range writeResp.Data.Children {
			if block == nil {
				continue
			}
			if blockID := stringValue(block.BlockId); blockID != "" {
				added = append(added, blockID)
			}
		}
		revision = intValue(writeResp.Data.DocumentRevisionId)
	}

	return &WriteDocumentResult{
		DocumentID:       documentID,
		DocumentRevision: revision,
		AddedBlockIDs:    added,
	}, nil
}

func (s *Service) ListDocuments(ctx context.Context, input ListDocumentsInput) (*ListDocumentsResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pageSize := input.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	reqBuilder := larkdrive.NewListFileReqBuilder().
		PageSize(pageSize)
	if folderToken := strings.TrimSpace(input.FolderToken); folderToken != "" {
		reqBuilder = reqBuilder.FolderToken(folderToken)
	}
	if pageToken := strings.TrimSpace(input.PageToken); pageToken != "" {
		reqBuilder = reqBuilder.PageToken(pageToken)
	}

	resp, err := s.client.Drive.V1.File.List(ctx, reqBuilder.Build())
	if err != nil {
		return nil, fmt.Errorf("列出文档失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("列出文档失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("列出文档失败", resp.Code, resp.Msg, logID)
	}

	result := &ListDocumentsResult{
		Documents: make([]Document, 0),
	}
	if resp.Data == nil {
		return result, nil
	}
	result.HasMore = boolValue(resp.Data.HasMore)
	result.NextPageToken = stringValue(resp.Data.NextPageToken)

	for _, file := range resp.Data.Files {
		if file == nil {
			continue
		}
		fileType := strings.ToLower(stringValue(file.Type))
		if fileType != larkdrive.FileTypeDoc && fileType != larkdrive.FileTypeDocx {
			continue
		}
		documentID := stringValue(file.Token)
		url := stringValue(file.Url)
		if url == "" && fileType == larkdrive.FileTypeDocx {
			url = s.documentURL(documentID)
		}
		result.Documents = append(result.Documents, Document{
			DocumentID: documentID,
			Title:      stringValue(file.Name),
			URL:        url,
		})
	}
	return result, nil
}

func (s *Service) documentURL(documentID string) string {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return ""
	}
	return defaultWebBaseURL + "/docx/" + documentID
}
