package feishudoc

import (
	"context"
	"fmt"
	"strings"

	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

const commentListPageSize = 50

type Comment struct {
	CommentID  string  `json:"comment_id"`
	Quote      string  `json:"quote,omitempty"`
	IsSolved   bool    `json:"is_solved"`
	Replies    []Reply `json:"replies,omitempty"`
	CreateTime int     `json:"create_time"`
	UpdateTime int     `json:"update_time"`
}

type Reply struct {
	ReplyID    string `json:"reply_id,omitempty"`
	Content    string `json:"content,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	CreateTime int    `json:"create_time"`
	UpdateTime int    `json:"update_time"`
}

func (s *Service) ListComments(ctx context.Context, token, tokenType string) ([]Comment, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	normalizedType, err := normalizeCommentTokenType(tokenType)
	if err != nil {
		return nil, err
	}

	comments := make([]Comment, 0)
	pageToken := ""
	for {
		reqBuilder := larkdrive.NewListFileCommentReqBuilder().
			FileToken(token).
			FileType(normalizedType).
			PageSize(commentListPageSize)
		if pageToken != "" {
			reqBuilder = reqBuilder.PageToken(pageToken)
		}

		resp, err := s.client.Drive.V1.FileComment.List(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("列出评论失败: %w", err)
		}
		if resp == nil {
			return nil, fmt.Errorf("列出评论失败: 响应为空")
		}
		if !resp.Success() {
			logID := ""
			if resp.Err != nil {
				logID = strings.TrimSpace(resp.Err.LogID)
			}
			return nil, newCodeError("列出评论失败", resp.Code, resp.Msg, logID)
		}

		if resp.Data != nil {
			for _, item := range resp.Data.Items {
				if item == nil {
					continue
				}
				comments = append(comments, toComment(item))
			}
			if !boolValue(resp.Data.HasMore) {
				break
			}
			next := stringValue(resp.Data.PageToken)
			if next == "" || next == pageToken {
				break
			}
			pageToken = next
			continue
		}
		break
	}

	return comments, nil
}

func (s *Service) GetComment(ctx context.Context, token, tokenType, commentID string) (*Comment, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return nil, fmt.Errorf("comment_id 不能为空")
	}
	normalizedType, err := normalizeCommentTokenType(tokenType)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Drive.V1.FileComment.Get(ctx,
		larkdrive.NewGetFileCommentReqBuilder().
			FileToken(token).
			CommentId(commentID).
			FileType(normalizedType).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("获取评论失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("获取评论失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("获取评论失败", resp.Code, resp.Msg, logID)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("获取评论失败: 缺少评论数据")
	}

	comment := toCommentFromGetData(resp.Data)
	return &comment, nil
}

func (s *Service) CreateComment(ctx context.Context, token, tokenType, content string) (*Comment, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("content 不能为空")
	}
	normalizedType, err := normalizeCommentTokenType(tokenType)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Drive.V1.FileComment.Create(ctx,
		larkdrive.NewCreateFileCommentReqBuilder().
			FileToken(token).
			FileType(normalizedType).
			FileComment(
				larkdrive.NewFileCommentBuilder().
					ReplyList(
						larkdrive.NewReplyListBuilder().
							Replies([]*larkdrive.FileCommentReply{
								larkdrive.NewFileCommentReplyBuilder().
									Content(buildCommentReplyContent(content)).
									Build(),
							}).
							Build(),
					).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("创建评论失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("创建评论失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("创建评论失败", resp.Code, resp.Msg, logID)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("创建评论失败: 缺少评论数据")
	}

	comment := toCommentFromCreateData(resp.Data)
	return &comment, nil
}

func (s *Service) ReplyComment(ctx context.Context, token, tokenType, commentID, content string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("token 不能为空")
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return fmt.Errorf("comment_id 不能为空")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("content 不能为空")
	}
	normalizedType, err := normalizeCommentTokenType(tokenType)
	if err != nil {
		return err
	}

	resp, err := s.client.Drive.V1.FileComment.Create(ctx,
		larkdrive.NewCreateFileCommentReqBuilder().
			FileToken(token).
			FileType(normalizedType).
			FileComment(
				larkdrive.NewFileCommentBuilder().
					CommentId(commentID).
					ReplyList(
						larkdrive.NewReplyListBuilder().
							Replies([]*larkdrive.FileCommentReply{
								larkdrive.NewFileCommentReplyBuilder().
									Content(buildCommentReplyContent(content)).
									Build(),
							}).
							Build(),
					).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("回复评论失败: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("回复评论失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return newCodeError("回复评论失败", resp.Code, resp.Msg, logID)
	}
	return nil
}

func (s *Service) ResolveComment(ctx context.Context, token, tokenType, commentID string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("token 不能为空")
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return fmt.Errorf("comment_id 不能为空")
	}
	normalizedType, err := normalizeCommentTokenType(tokenType)
	if err != nil {
		return err
	}

	resp, err := s.client.Drive.V1.FileComment.Patch(ctx,
		larkdrive.NewPatchFileCommentReqBuilder().
			FileToken(token).
			CommentId(commentID).
			FileType(normalizedType).
			Body(
				larkdrive.NewPatchFileCommentReqBodyBuilder().
					IsSolved(true).
					Build(),
			).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("解决评论失败: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("解决评论失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return newCodeError("解决评论失败", resp.Code, resp.Msg, logID)
	}
	return nil
}

func normalizeCommentTokenType(raw string) (string, error) {
	tokenType, err := normalizePermissionTokenType(raw)
	if err != nil {
		return "", err
	}
	switch tokenType {
	case larkdrive.FileTypeListFileCommentDoc,
		larkdrive.FileTypeListFileCommentDocx,
		larkdrive.FileTypeListFileCommentSheet,
		larkdrive.FileTypeListFileCommentFile,
		larkdrive.FileTypeListFileCommentSlides:
		return tokenType, nil
	default:
		return "", fmt.Errorf("comment 不支持的 token type: %s", strings.TrimSpace(raw))
	}
}

func buildCommentReplyContent(content string) *larkdrive.ReplyContent {
	return larkdrive.NewReplyContentBuilder().
		Elements([]*larkdrive.ReplyElement{
			larkdrive.NewReplyElementBuilder().
				Type("text_run").
				TextRun(
					larkdrive.NewTextRunBuilder().
						Text(content).
						Build(),
				).
				Build(),
		}).
		Build()
}

func toComment(item *larkdrive.FileComment) Comment {
	if item == nil {
		return Comment{}
	}
	return buildComment(
		stringValue(item.CommentId),
		stringValue(item.Quote),
		boolValue(item.IsSolved),
		intValue(item.CreateTime),
		intValue(item.UpdateTime),
		toReplies(item.ReplyList),
	)
}

func toCommentFromGetData(data *larkdrive.GetFileCommentRespData) Comment {
	if data == nil {
		return Comment{}
	}
	return buildComment(
		stringValue(data.CommentId),
		stringValue(data.Quote),
		boolValue(data.IsSolved),
		intValue(data.CreateTime),
		intValue(data.UpdateTime),
		toReplies(data.ReplyList),
	)
}

func toCommentFromCreateData(data *larkdrive.CreateFileCommentRespData) Comment {
	if data == nil {
		return Comment{}
	}
	return buildComment(
		stringValue(data.CommentId),
		stringValue(data.Quote),
		boolValue(data.IsSolved),
		intValue(data.CreateTime),
		intValue(data.UpdateTime),
		toReplies(data.ReplyList),
	)
}

func buildComment(commentID, quote string, isSolved bool, createTime, updateTime int, replies []Reply) Comment {
	return Comment{
		CommentID:  commentID,
		Quote:      quote,
		IsSolved:   isSolved,
		Replies:    replies,
		CreateTime: createTime,
		UpdateTime: updateTime,
	}
}

func toReplies(replyList *larkdrive.ReplyList) []Reply {
	if replyList == nil || len(replyList.Replies) == 0 {
		return nil
	}
	replies := make([]Reply, 0, len(replyList.Replies))
	for _, item := range replyList.Replies {
		if item == nil {
			continue
		}
		replies = append(replies, toReply(item))
	}
	if len(replies) == 0 {
		return nil
	}
	return replies
}

func toReply(item *larkdrive.FileCommentReply) Reply {
	if item == nil {
		return Reply{}
	}
	return Reply{
		ReplyID:    stringValue(item.ReplyId),
		Content:    extractReplyContent(item.Content),
		UserID:     stringValue(item.UserId),
		CreateTime: intValue(item.CreateTime),
		UpdateTime: intValue(item.UpdateTime),
	}
}

func extractReplyContent(content *larkdrive.ReplyContent) string {
	if content == nil || len(content.Elements) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, element := range content.Elements {
		builder.WriteString(extractReplyElementText(element))
	}
	return strings.TrimSpace(builder.String())
}

func extractReplyElementText(element *larkdrive.ReplyElement) string {
	if element == nil {
		return ""
	}
	if element.TextRun != nil {
		if element.TextRun.Text == nil {
			return ""
		}
		return *element.TextRun.Text
	}
	if element.DocsLink != nil {
		return stringValue(element.DocsLink.Url)
	}
	if element.Person != nil {
		userID := stringValue(element.Person.UserId)
		if userID == "" {
			return ""
		}
		return "@" + userID
	}
	return ""
}
