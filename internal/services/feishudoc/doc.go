package feishudoc

import (
	"context"
	"fmt"
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

type ReadDocumentResult struct {
	Document Document `json:"document"`
	Content  string   `json:"content"`
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

func (s *Service) ReadDocument(ctx context.Context, documentID string) (*ReadDocumentResult, error) {
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

	rawResp, err := s.client.Docx.V1.Document.RawContent(ctx,
		larkdocx.NewRawContentDocumentReqBuilder().
			DocumentId(documentID).
			Lang(larkdocx.LangZH).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("读取文档内容失败: %w", err)
	}
	if rawResp == nil {
		return nil, fmt.Errorf("读取文档内容失败: 响应为空")
	}
	if !rawResp.Success() {
		logID := ""
		if rawResp.Err != nil {
			logID = strings.TrimSpace(rawResp.Err.LogID)
		}
		return nil, newCodeError("读取文档内容失败", rawResp.Code, rawResp.Msg, logID)
	}

	title := ""
	revision := 0
	if getResp.Data != nil && getResp.Data.Document != nil {
		title = stringValue(getResp.Data.Document.Title)
		revision = intValue(getResp.Data.Document.RevisionId)
	}
	content := ""
	if rawResp.Data != nil {
		content = stringValue(rawResp.Data.Content)
	}

	return &ReadDocumentResult{
		Document: Document{
			DocumentID: documentID,
			Title:      title,
			RevisionID: revision,
			URL:        s.documentURL(documentID),
		},
		Content: content,
	}, nil
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
	base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/")
	if base == "" {
		base = defaultOpenBaseURL
	}
	return base + "/docx/" + documentID
}
