package feishudoc

import (
	"context"
	"fmt"
	"strings"

	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

type UpdatePublicShareInput struct {
	Token           string
	TokenType       string
	LinkShareEntity string
	ExternalAccess  *bool
	SecurityEntity  string
	CommentEntity   string
	ShareEntity     string
	InviteExternal  *bool
}

type PublicPermission struct {
	ExternalAccess  bool   `json:"external_access"`
	SecurityEntity  string `json:"security_entity,omitempty"`
	CommentEntity   string `json:"comment_entity,omitempty"`
	ShareEntity     string `json:"share_entity,omitempty"`
	LinkShareEntity string `json:"link_share_entity,omitempty"`
	InviteExternal  bool   `json:"invite_external"`
}

type UpdatePublicShareResult struct {
	Token      string           `json:"token"`
	TokenType  string           `json:"token_type"`
	URL        string           `json:"url,omitempty"`
	Permission PublicPermission `json:"permission"`
}

type AddPermissionMemberInput struct {
	Token            string
	TokenType        string
	MemberType       string
	MemberID         string
	Perm             string
	PermType         string
	CollaboratorType string
	NeedNotification bool
}

type PermissionMember struct {
	MemberType    string `json:"member_type,omitempty"`
	MemberID      string `json:"member_id,omitempty"`
	Perm          string `json:"perm,omitempty"`
	PermType      string `json:"perm_type,omitempty"`
	Type          string `json:"type,omitempty"`
	Name          string `json:"name,omitempty"`
	Avatar        string `json:"avatar,omitempty"`
	ExternalLabel bool   `json:"external_label"`
}

type ListPermissionMembersInput struct {
	Token     string
	TokenType string
	Fields    string
	PermType  string
}

type ListPermissionMembersResult struct {
	Members []PermissionMember `json:"members"`
}

func (s *Service) UpdatePublicShare(ctx context.Context, input UpdatePublicShareInput) (*UpdatePublicShareResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token := strings.TrimSpace(input.Token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	tokenType, err := normalizePermissionTokenType(input.TokenType)
	if err != nil {
		return nil, err
	}
	linkShareEntity, err := normalizeLinkShareEntity(input.LinkShareEntity)
	if err != nil {
		return nil, err
	}
	securityEntity, err := normalizeSecurityEntity(input.SecurityEntity)
	if err != nil {
		return nil, err
	}
	commentEntity, err := normalizeCommentEntity(input.CommentEntity)
	if err != nil {
		return nil, err
	}
	shareEntity, err := normalizeShareEntity(input.ShareEntity)
	if err != nil {
		return nil, err
	}

	permissionBuilder := larkdrive.NewPermissionPublicRequestBuilder().
		LinkShareEntity(linkShareEntity)
	if input.ExternalAccess != nil {
		permissionBuilder = permissionBuilder.ExternalAccess(*input.ExternalAccess)
	}
	if securityEntity != "" {
		permissionBuilder = permissionBuilder.SecurityEntity(securityEntity)
	}
	if commentEntity != "" {
		permissionBuilder = permissionBuilder.CommentEntity(commentEntity)
	}
	if shareEntity != "" {
		permissionBuilder = permissionBuilder.ShareEntity(shareEntity)
	}
	if input.InviteExternal != nil {
		permissionBuilder = permissionBuilder.InviteExternal(*input.InviteExternal)
	}

	resp, err := s.client.Drive.V1.PermissionPublic.Patch(ctx,
		larkdrive.NewPatchPermissionPublicReqBuilder().
			Token(token).
			Type(tokenType).
			PermissionPublicRequest(permissionBuilder.Build()).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("更新公开分享权限失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("更新公开分享权限失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("更新公开分享权限失败", resp.Code, resp.Msg, logID)
	}

	permission := PublicPermission{}
	if resp.Data != nil {
		permission = toPublicPermission(resp.Data.PermissionPublic)
	}
	return &UpdatePublicShareResult{
		Token:      token,
		TokenType:  tokenType,
		URL:        s.tokenURL(tokenType, token),
		Permission: permission,
	}, nil
}

func (s *Service) AddPermissionMember(ctx context.Context, input AddPermissionMemberInput) (*PermissionMember, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token := strings.TrimSpace(input.Token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	tokenType, err := normalizePermissionTokenType(input.TokenType)
	if err != nil {
		return nil, err
	}
	memberType, err := normalizePermissionMemberType(input.MemberType)
	if err != nil {
		return nil, err
	}
	memberID := strings.TrimSpace(input.MemberID)
	if memberID == "" {
		return nil, fmt.Errorf("member_id 不能为空")
	}
	perm, err := normalizePermissionRole(input.Perm)
	if err != nil {
		return nil, err
	}
	permType, err := normalizePermissionRoleType(input.PermType)
	if err != nil {
		return nil, err
	}
	collaboratorType, err := normalizeCollaboratorType(input.CollaboratorType)
	if err != nil {
		return nil, err
	}

	baseMember := larkdrive.NewBaseMemberBuilder().
		MemberType(memberType).
		MemberId(memberID).
		Perm(perm).
		PermType(permType).
		Type(collaboratorType).
		Build()

	resp, err := s.client.Drive.V1.PermissionMember.Create(ctx,
		larkdrive.NewCreatePermissionMemberReqBuilder().
			Token(token).
			Type(tokenType).
			NeedNotification(input.NeedNotification).
			BaseMember(baseMember).
			Build(),
	)
	if err != nil {
		return nil, fmt.Errorf("添加协作者失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("添加协作者失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("添加协作者失败", resp.Code, resp.Msg, logID)
	}
	if resp.Data == nil || resp.Data.Member == nil {
		return nil, fmt.Errorf("添加协作者失败: 缺少 member 数据")
	}

	member := toPermissionMemberFromBase(resp.Data.Member)
	return &member, nil
}

func (s *Service) ListPermissionMembers(ctx context.Context, input ListPermissionMembersInput) (*ListPermissionMembersResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token := strings.TrimSpace(input.Token)
	if token == "" {
		return nil, fmt.Errorf("token 不能为空")
	}
	tokenType, err := normalizePermissionTokenType(input.TokenType)
	if err != nil {
		return nil, err
	}
	fields := strings.TrimSpace(input.Fields)
	if fields == "" {
		fields = "name,type,avatar,external_label"
	}
	permType, err := normalizePermissionRoleTypeAllowEmpty(input.PermType)
	if err != nil {
		return nil, err
	}

	reqBuilder := larkdrive.NewListPermissionMemberReqBuilder().
		Token(token).
		Type(tokenType).
		Fields(fields)
	if permType != "" {
		reqBuilder = reqBuilder.PermType(permType)
	}

	resp, err := s.client.Drive.V1.PermissionMember.List(ctx, reqBuilder.Build())
	if err != nil {
		return nil, fmt.Errorf("列出协作者失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("列出协作者失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("列出协作者失败", resp.Code, resp.Msg, logID)
	}

	result := &ListPermissionMembersResult{Members: make([]PermissionMember, 0)}
	if resp.Data == nil {
		return result, nil
	}
	for _, item := range resp.Data.Items {
		if item == nil {
			continue
		}
		result.Members = append(result.Members, toPermissionMember(item))
	}
	return result, nil
}

func normalizePermissionTokenType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.TokenTypeV2Docx, nil
	}
	switch v {
	case larkdrive.TokenTypeV2Doc,
		larkdrive.TokenTypeV2Docx,
		larkdrive.TokenTypeV2Sheet,
		larkdrive.TokenTypeV2File,
		larkdrive.TokenTypeV2Wiki,
		larkdrive.TokenTypeV2Bitable,
		larkdrive.TokenTypeV2Folder,
		larkdrive.TokenTypeV2Mindnote,
		larkdrive.TokenTypeV2Minutes,
		larkdrive.TokenTypeV2Slides:
		return v, nil
	case "document":
		return larkdrive.TokenTypeV2Docx, nil
	default:
		return "", fmt.Errorf("不支持的 token type: %s", strings.TrimSpace(raw))
	}
}

func normalizeLinkShareEntity(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.LinkShareEntityTenantEditable, nil
	}
	switch v {
	case larkdrive.LinkShareEntityTenantReadable,
		larkdrive.LinkShareEntityTenantEditable,
		larkdrive.LinkShareEntityAnyoneReadable,
		larkdrive.LinkShareEntityAnyoneEditable,
		larkdrive.LinkShareEntityClosed:
		return v, nil
	case "tenant_read", "tenant-readable":
		return larkdrive.LinkShareEntityTenantReadable, nil
	case "tenant_edit", "tenant-editable":
		return larkdrive.LinkShareEntityTenantEditable, nil
	case "anyone_read", "anyone-readable":
		return larkdrive.LinkShareEntityAnyoneReadable, nil
	case "anyone_edit", "anyone-editable":
		return larkdrive.LinkShareEntityAnyoneEditable, nil
	default:
		return "", fmt.Errorf("不支持的 link_share_entity: %s", strings.TrimSpace(raw))
	}
}

func normalizeSecurityEntity(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	switch v {
	case larkdrive.SecurityEntityAnyoneCanView,
		larkdrive.SecurityEntityAnyoneCanEdit,
		larkdrive.SecurityEntityOnlyFullAccess:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 security_entity: %s", strings.TrimSpace(raw))
	}
}

func normalizeCommentEntity(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	switch v {
	case larkdrive.CommentEntityAnyoneCanView,
		larkdrive.CommentEntityAnyoneCanEdit:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 comment_entity: %s", strings.TrimSpace(raw))
	}
}

func normalizeShareEntity(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	switch v {
	case larkdrive.ShareEntityAnyone,
		larkdrive.ShareEntitySameTenant,
		larkdrive.ShareEntityOnlyFullAccess:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 share_entity: %s", strings.TrimSpace(raw))
	}
}

func normalizePermissionMemberType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.MemberTypeOpenId, nil
	}
	switch v {
	case larkdrive.MemberTypeEmail,
		larkdrive.MemberTypeOpenId,
		larkdrive.MemberTypeUnionId,
		larkdrive.MemberTypeOpenChat,
		larkdrive.MemberTypeOpenDepartmentId,
		larkdrive.MemberTypeUserId,
		larkdrive.MemberTypeGroupId,
		larkdrive.MemberTypeWikiSpaceId:
		return v, nil
	case "open_id":
		return larkdrive.MemberTypeOpenId, nil
	case "user_id":
		return larkdrive.MemberTypeUserId, nil
	default:
		return "", fmt.Errorf("不支持的 member_type: %s", strings.TrimSpace(raw))
	}
}

func normalizePermissionRole(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.PermCreatePermissionMemberEdit, nil
	}
	switch v {
	case larkdrive.PermCreatePermissionMemberView,
		larkdrive.PermCreatePermissionMemberEdit,
		larkdrive.PermCreatePermissionMemberFullAccess:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 perm: %s", strings.TrimSpace(raw))
	}
}

func normalizePermissionRoleType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.PermTypeContainer, nil
	}
	switch v {
	case larkdrive.PermTypeContainer,
		larkdrive.PermTypeSinglePage:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 perm_type: %s", strings.TrimSpace(raw))
	}
}

func normalizePermissionRoleTypeAllowEmpty(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	return normalizePermissionRoleType(v)
}

func normalizeCollaboratorType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return larkdrive.TypeCreatePermissionMemberUser, nil
	}
	switch v {
	case larkdrive.TypeCreatePermissionMemberUser,
		larkdrive.TypeCreatePermissionMemberChat,
		larkdrive.TypeCreatePermissionMemberDepartment,
		larkdrive.TypeCreatePermissionMemberGroup,
		larkdrive.TypeCreatePermissionMemberWikiSpaceMember,
		larkdrive.TypeCreatePermissionMemberWikiSpaceViewer,
		larkdrive.TypeCreatePermissionMemberWikiSpaceEditor:
		return v, nil
	default:
		return "", fmt.Errorf("不支持的 collaborator type: %s", strings.TrimSpace(raw))
	}
}

func toPublicPermission(permission *larkdrive.PermissionPublic) PublicPermission {
	if permission == nil {
		return PublicPermission{}
	}
	return PublicPermission{
		ExternalAccess:  boolValue(permission.ExternalAccess),
		SecurityEntity:  stringValue(permission.SecurityEntity),
		CommentEntity:   stringValue(permission.CommentEntity),
		ShareEntity:     stringValue(permission.ShareEntity),
		LinkShareEntity: stringValue(permission.LinkShareEntity),
		InviteExternal:  boolValue(permission.InviteExternal),
	}
}

func toPermissionMemberFromBase(member *larkdrive.BaseMember) PermissionMember {
	if member == nil {
		return PermissionMember{}
	}
	return PermissionMember{
		MemberType: stringValue(member.MemberType),
		MemberID:   stringValue(member.MemberId),
		Perm:       stringValue(member.Perm),
		PermType:   stringValue(member.PermType),
		Type:       stringValue(member.Type),
	}
}

func toPermissionMember(member *larkdrive.Member) PermissionMember {
	if member == nil {
		return PermissionMember{}
	}
	return PermissionMember{
		MemberType:    stringValue(member.MemberType),
		MemberID:      stringValue(member.MemberId),
		Perm:          stringValue(member.Perm),
		PermType:      stringValue(member.PermType),
		Type:          stringValue(member.Type),
		Name:          stringValue(member.Name),
		Avatar:        stringValue(member.Avatar),
		ExternalLabel: boolValue(member.ExternalLabel),
	}
}

func (s *Service) tokenURL(tokenType, token string) string {
	tokenType = strings.ToLower(strings.TrimSpace(tokenType))
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/")
	if base == "" {
		base = defaultOpenBaseURL
	}
	switch tokenType {
	case larkdrive.TokenTypeV2Docx:
		return base + "/docx/" + token
	case larkdrive.TokenTypeV2Doc:
		return base + "/docs/" + token
	case larkdrive.TokenTypeV2Wiki:
		return base + "/wiki/" + token
	case larkdrive.TokenTypeV2Sheet:
		return base + "/sheets/" + token
	case larkdrive.TokenTypeV2Slides:
		return base + "/slides/" + token
	case larkdrive.TokenTypeV2Bitable:
		return base + "/base/" + token
	case larkdrive.TokenTypeV2Mindnote:
		return base + "/mindnotes/" + token
	case larkdrive.TokenTypeV2File, larkdrive.TokenTypeV2Folder:
		return base + "/file/" + token
	default:
		return ""
	}
}
