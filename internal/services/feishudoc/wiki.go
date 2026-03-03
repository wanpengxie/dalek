package feishudoc

import (
	"context"
	"fmt"
	"strings"

	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
)

const (
	defaultWikiPageSize = 50
	maxWikiPageSize     = 200
)

type ListWikiSpacesInput struct {
	PageToken string
	PageSize  int
}

type WikiSpace struct {
	SpaceID     string `json:"space_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SpaceType   string `json:"space_type,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	OpenSharing string `json:"open_sharing,omitempty"`
}

type ListWikiSpacesResult struct {
	Spaces        []WikiSpace `json:"spaces"`
	HasMore       bool        `json:"has_more"`
	NextPageToken string      `json:"next_page_token,omitempty"`
}

type ListWikiNodesInput struct {
	SpaceID         string
	ParentNodeToken string
	PageToken       string
	PageSize        int
}

type WikiNode struct {
	SpaceID         string `json:"space_id,omitempty"`
	NodeToken       string `json:"node_token"`
	ParentNodeToken string `json:"parent_node_token,omitempty"`
	ObjType         string `json:"obj_type,omitempty"`
	ObjToken        string `json:"obj_token,omitempty"`
	Title           string `json:"title,omitempty"`
	NodeType        string `json:"node_type,omitempty"`
	HasChild        bool   `json:"has_child"`
}

type ListWikiNodesResult struct {
	Nodes         []WikiNode `json:"nodes"`
	HasMore       bool       `json:"has_more"`
	NextPageToken string     `json:"next_page_token,omitempty"`
}

type CreateWikiNodeInput struct {
	SpaceID         string
	ParentNodeToken string
	ObjType         string
	ObjToken        string
	Title           string
}

func (s *Service) ListWikiSpaces(ctx context.Context, input ListWikiSpacesInput) (*ListWikiSpacesResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqBuilder := larkwiki.NewListSpaceReqBuilder().
		PageSize(normalizeWikiPageSize(input.PageSize))
	if pageToken := strings.TrimSpace(input.PageToken); pageToken != "" {
		reqBuilder = reqBuilder.PageToken(pageToken)
	}

	resp, err := s.client.Wiki.V2.Space.List(ctx, reqBuilder.Build())
	if err != nil {
		return nil, fmt.Errorf("列出知识空间失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("列出知识空间失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("列出知识空间失败", resp.Code, resp.Msg, logID)
	}

	result := &ListWikiSpacesResult{Spaces: make([]WikiSpace, 0)}
	if resp.Data == nil {
		return result, nil
	}
	result.HasMore = boolValue(resp.Data.HasMore)
	result.NextPageToken = stringValue(resp.Data.PageToken)
	for _, item := range resp.Data.Items {
		if item == nil {
			continue
		}
		result.Spaces = append(result.Spaces, WikiSpace{
			SpaceID:     stringValue(item.SpaceId),
			Name:        stringValue(item.Name),
			Description: stringValue(item.Description),
			SpaceType:   stringValue(item.SpaceType),
			Visibility:  stringValue(item.Visibility),
			OpenSharing: stringValue(item.OpenSharing),
		})
	}
	return result, nil
}

func (s *Service) ListWikiNodes(ctx context.Context, input ListWikiNodesInput) (*ListWikiNodesResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	spaceID := strings.TrimSpace(input.SpaceID)
	if spaceID == "" {
		return nil, fmt.Errorf("space_id 不能为空")
	}

	reqBuilder := larkwiki.NewListSpaceNodeReqBuilder().
		SpaceId(spaceID).
		PageSize(normalizeWikiPageSize(input.PageSize))
	if pageToken := strings.TrimSpace(input.PageToken); pageToken != "" {
		reqBuilder = reqBuilder.PageToken(pageToken)
	}
	if parent := strings.TrimSpace(input.ParentNodeToken); parent != "" {
		reqBuilder = reqBuilder.ParentNodeToken(parent)
	}

	resp, err := s.client.Wiki.V2.SpaceNode.List(ctx, reqBuilder.Build())
	if err != nil {
		return nil, fmt.Errorf("列出知识节点失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("列出知识节点失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("列出知识节点失败", resp.Code, resp.Msg, logID)
	}

	result := &ListWikiNodesResult{Nodes: make([]WikiNode, 0)}
	if resp.Data == nil {
		return result, nil
	}
	result.HasMore = boolValue(resp.Data.HasMore)
	result.NextPageToken = stringValue(resp.Data.PageToken)
	for _, item := range resp.Data.Items {
		if item == nil {
			continue
		}
		result.Nodes = append(result.Nodes, toWikiNode(item))
	}
	return result, nil
}

func (s *Service) CreateWikiNode(ctx context.Context, input CreateWikiNodeInput) (*WikiNode, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	spaceID := strings.TrimSpace(input.SpaceID)
	if spaceID == "" {
		return nil, fmt.Errorf("space_id 不能为空")
	}

	objType, err := normalizeWikiObjType(input.ObjType)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(input.Title)
	objToken := strings.TrimSpace(input.ObjToken)
	if title == "" && objToken == "" {
		return nil, fmt.Errorf("title 与 obj_token 不能同时为空")
	}

	nodeBuilder := larkwiki.NewNodeBuilder().ObjType(objType)
	if parent := strings.TrimSpace(input.ParentNodeToken); parent != "" {
		nodeBuilder = nodeBuilder.ParentNodeToken(parent)
	}
	if title != "" {
		nodeBuilder = nodeBuilder.Title(title)
	}
	if objToken != "" {
		nodeBuilder = nodeBuilder.ObjToken(objToken)
	}

	resp, err := s.client.Wiki.V2.SpaceNode.Create(ctx,
		larkwiki.NewCreateSpaceNodeReqBuilder().
			SpaceId(spaceID).
			Node(nodeBuilder.Build()).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("创建知识节点失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("创建知识节点失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("创建知识节点失败", resp.Code, resp.Msg, logID)
	}
	if resp.Data == nil || resp.Data.Node == nil {
		return nil, fmt.Errorf("创建知识节点失败: 缺少 node 数据")
	}

	node := toWikiNode(resp.Data.Node)
	if node.NodeToken == "" {
		return nil, fmt.Errorf("创建知识节点失败: node_token 为空")
	}
	return &node, nil
}

func normalizeWikiPageSize(pageSize int) int {
	if pageSize <= 0 {
		return defaultWikiPageSize
	}
	if pageSize > maxWikiPageSize {
		return maxWikiPageSize
	}
	return pageSize
}

func normalizeWikiObjType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkwiki.ObjTypeObjTypeDocx, nil
	}
	switch v {
	case larkwiki.ObjTypeObjTypeDoc,
		larkwiki.ObjTypeObjTypeDocx,
		larkwiki.ObjTypeObjTypeSheet,
		larkwiki.ObjTypeObjTypeMindNote,
		larkwiki.ObjTypeObjTypeBitable,
		larkwiki.ObjTypeObjTypeFile,
		larkwiki.ObjTypeObjTypeSlides,
		larkwiki.ObjTypeForQueryObjTypeWiki:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 obj_type: %s", strings.TrimSpace(raw))
	}
}

func toWikiNode(node *larkwiki.Node) WikiNode {
	if node == nil {
		return WikiNode{}
	}
	return WikiNode{
		SpaceID:         stringValue(node.SpaceId),
		NodeToken:       stringValue(node.NodeToken),
		ParentNodeToken: stringValue(node.ParentNodeToken),
		ObjType:         stringValue(node.ObjType),
		ObjToken:        stringValue(node.ObjToken),
		Title:           stringValue(node.Title),
		NodeType:        stringValue(node.NodeType),
		HasChild:        boolValue(node.HasChild),
	}
}
