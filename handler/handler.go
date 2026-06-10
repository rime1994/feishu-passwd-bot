package handler

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"unicode"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"feishu-passwd-bot/card"
	"feishu-passwd-bot/config"
	botldap "feishu-passwd-bot/ldap"
)

// 触发关键词
var triggerKeywords = []string{"修改密码", "改密码", "重置密码", "changepassword", "change password"}

type Handler struct {
	client     *lark.Client
	ldapClient *botldap.Client
	cfg        *config.Config
}

func New(cfg *config.Config, ldapClient *botldap.Client) *Handler {
	return &Handler{
		client:     lark.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret),
		ldapClient: ldapClient,
		cfg:        cfg,
	}
}

// OnMessage 处理用户发给 Bot 的消息
func (h *Handler) OnMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
		return nil
	}
	openID := strVal(event.Event.Sender.SenderId.OpenId)
	if openID == "" {
		return nil
	}

	// 提取消息文本
	text := extractMsgText(event.Event.Message)

	// 检查是否触发关键词
	if !isTriggered(text) {
		return h.sendText(ctx, openID, "发送「修改密码」开始自助修改 LDAP 密码 🔐")
	}

	feishuUserID, err := h.resolveUserID(ctx, openID)
	if err != nil {
		log.Printf("[handler] resolveUserID failed open_id=%s err=%v", openID, err)
	}
	ldapUID := ""
	if feishuUserID != "" {
		log.Printf("[handler] resolved user_id=%s for open_id=%s", feishuUserID, openID)
		if u, err := h.ldapClient.FindUser(feishuUserID); err != nil {
			log.Printf("[handler] FindUser failed user_id=%s err=%v", feishuUserID, err)
		} else if u != nil {
			ldapUID = u.UID
			log.Printf("[handler] LDAP uid=%s for user_id=%s", ldapUID, feishuUserID)
		} else {
			log.Printf("[handler] no LDAP user found for user_id=%s", feishuUserID)
		}
	} else {
		log.Printf("[handler] feishuUserID empty for open_id=%s", openID)
	}
	return h.sendCard(ctx, openID, card.PasswordFormCard(ldapUID))
}

// OnCardAction 处理交互卡片回调（用户提交表单）
func (h *Handler) OnCardAction(ctx context.Context, action *larkcard.CardAction) (any, error) {
	actionVal, _ := action.Action.Value["action"].(string)
	openID := action.OpenID
	// user_id is sent directly in the card callback; avoids an extra Contact API round-trip.
	userID := action.UserID
	msgID := action.OpenMessageID

	switch actionVal {
	case "cancel":
		h.updateCard(ctx, msgID, card.CancelCard())
		return h.toast("info", "已取消"), nil

	case "retry":
		h.updateCard(ctx, msgID, card.PasswordFormCard(""))
		return h.toast("info", "请重新填写"), nil

	case "submit_password":
		return h.handleSubmit(ctx, openID, userID, msgID, action.Action.FormValue)

	default:
		return nil, nil
	}
}

// updateCard 通过 Im.Message.Patch 替换原始卡片消息（schema 2.0 回调不支持在 response body 里直接更新卡片）
func (h *Handler) updateCard(ctx context.Context, msgID, cardJSON string) {
	if msgID == "" {
		return
	}
	_, err := h.client.Im.Message.Patch(ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(msgID).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(cardJSON).
				Build()).
			Build(),
	)
	if err != nil {
		log.Printf("[handler] updateCard failed msg_id=%s err=%v", msgID, err)
	}
}

// toast 构造飞书卡片 toast 响应（schema 2.0 回调响应仅支持 toast）
func (h *Handler) toast(typ, content string) *larkcard.CustomResp {
	return &larkcard.CustomResp{
		StatusCode: 200,
		Body: map[string]any{
			"toast": map[string]any{
				"type":    typ,
				"content": content,
			},
		},
	}
}

// handleSubmit 处理密码提交
func (h *Handler) handleSubmit(ctx context.Context, openID, userID, msgID string, formValue map[string]any) (any, error) {
	newPass, _ := formValue["new_password"].(string)
	confirmPass, _ := formValue["confirm_password"].(string)

	// 密码校验
	if err := validatePassword(newPass, confirmPass); err != nil {
		h.updateCard(ctx, msgID, card.ErrorCard(err.Error()))
		return h.toast("error", err.Error()), nil
	}

	// 优先使用卡片回调中的 user_id，否则调 Contact API 解析
	if userID == "" {
		var err error
		userID, err = h.resolveUserID(ctx, openID)
		if err != nil || userID == "" {
			log.Printf("[handler] 获取 user_id 失败 open_id=%s err=%v", openID, err)
			h.updateCard(ctx, msgID, card.ErrorCard("无法获取您的账号信息，请联系管理员"))
			return h.toast("error", "获取账号信息失败"), nil
		}
	}

	userDN, err := h.ldapClient.FindUserDN(userID)
	if err != nil {
		log.Printf("[handler] LDAP 查询失败 user_id=%s err=%v", userID, err)
		h.updateCard(ctx, msgID, card.ErrorCard("查询 LDAP 账号失败，请稍后重试"))
		return h.toast("error", "查询账号失败"), nil
	}
	if userDN == "" {
		h.updateCard(ctx, msgID, card.ErrorCard("未找到您的 LDAP 账号，请确认已完成飞书同步"))
		return h.toast("error", "未找到 LDAP 账号"), nil
	}

	if err := h.ldapClient.ChangePassword(userDN, newPass); err != nil {
		log.Printf("[handler] 改密失败 dn=%s err=%v", userDN, err)
		h.updateCard(ctx, msgID, card.ErrorCard("密码修改失败，请稍后重试"))
		return h.toast("error", "密码修改失败"), nil
	}

	log.Printf("[handler] 密码修改成功 dn=%s", userDN)
	h.updateCard(ctx, msgID, card.SuccessCard())
	return h.toast("success", "密码修改成功"), nil
}

// resolveUserID 通过 open_id 获取飞书 user_id
// 需要应用有 contact:user.id:readonly 权限
func (h *Handler) resolveUserID(ctx context.Context, openID string) (string, error) {
	resp, err := h.client.Contact.User.Get(ctx,
		larkcontact.NewGetUserReqBuilder().
			UserId(openID).
			UserIdType("open_id").
			Build(),
	)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", nil
	}
	if resp.Data == nil || resp.Data.User == nil || resp.Data.User.UserId == nil {
		return "", nil
	}
	return *resp.Data.User.UserId, nil
}

// sendCard 发送交互卡片给用户
func (h *Handler) sendCard(ctx context.Context, openID, cardJSON string) error {
	_, err := h.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("interactive").
				Content(cardJSON).
				Build()).
			Build(),
	)
	return err
}

// sendText 发送文本消息给用户
func (h *Handler) sendText(ctx context.Context, openID, text string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	_, err := h.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("text").
				Content(string(content)).
				Build()).
			Build(),
	)
	return err
}

// validatePassword 校验密码强度
func validatePassword(newPass, confirmPass string) error {
	if newPass == "" || confirmPass == "" {
		return errorf("密码不能为空")
	}
	if newPass != confirmPass {
		return errorf("两次输入的密码不一致")
	}
	if len([]rune(newPass)) < 8 {
		return errorf("密码至少需要 8 位")
	}
	hasLetter, hasDigit := false, false
	for _, r := range newPass {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return errorf("密码必须同时包含字母和数字")
	}
	return nil
}

// extractMsgText 从飞书消息 content JSON 中提取纯文本
func extractMsgText(msg *larkim.EventMessage) string {
	if msg == nil || msg.Content == nil {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*msg.Content), &m); err != nil {
		return ""
	}
	return m["text"]
}

func isTriggered(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, kw := range triggerKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type simpleError string

func (e simpleError) Error() string { return string(e) }
func errorf(msg string) error       { return simpleError(msg) }
